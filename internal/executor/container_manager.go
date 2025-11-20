package executor

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/lmittmann/tint"
)

const (
	StateIdle    container.ContainerState = "idle"
	StateBusy    container.ContainerState = "busy"
	StateError   container.ContainerState = "error"
	StateRunning container.ContainerState = "running"

	MB int64 = 1024 * 1024

	maxRetries   int = 10
	retryDelayMS int = 200
)

var (
	ErrContainerNotFound error = errors.New("Container not found")
)

type ContainerInfo struct {
	ID    string
	State container.ContainerState
}

type DockerContainerManager struct {
	mu            sync.Mutex
	logger        *slog.Logger // this logger both writes to terminal and to log file
	cli           *client.Client
	containers    map[string]*ContainerInfo
	maxWorkers    int
	memoryLimitMB int64
	cpunanoLimit  int64
}

func NewDockerClient() (*client.Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return cli, nil
}

func NewDockerContainerManager(maxWorkers int, memoryLimitMB int64, cpunanoLimit int64) (*DockerContainerManager, error) {
	dockerClient, err := NewDockerClient()
	if err != nil {
		return nil, err
	}

	// Create logs directory if it doesn't exist
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, err
	}

	// write to both terminal and log file
	logFile, err := os.OpenFile("logs/container.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	multiWriter := io.MultiWriter(os.Stdout, logFile)
	slogHandler := tint.NewHandler(multiWriter, &tint.Options{Level: slog.LevelDebug, AddSource: true})
	logger := slog.New(slogHandler)
	return &DockerContainerManager{
		logger:        logger,
		cli:           dockerClient,
		containers:    make(map[string]*ContainerInfo),
		maxWorkers:    maxWorkers,
		cpunanoLimit:  cpunanoLimit,
		memoryLimitMB: memoryLimitMB,
	}, nil
}

func (d *DockerContainerManager) InitializePool() error {
	ctx := context.Background()
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		d.logger.Error("failed to list out containers", "err", err)
		return err
	}

	// Register existing worker container
	for _, c := range containers {
		if c.Image == "songphuc/worker" {
			state := StateIdle
			if c.State != StateRunning {
				state = StateError
			}

			d.mu.Lock()
			d.containers[c.ID] = &ContainerInfo{
				ID:    c.ID,
				State: state,
			}
			d.mu.Unlock()

			d.logger.Info("Worker container found",
				"container_id", c.ID,
				"container_state", state)
		}
	}

	d.balanceWorker()

	return nil
}

func (d *DockerContainerManager) StartContainer() error {
	ctx := context.Background()

	d.mu.Lock()
	if len(d.containers) >= d.maxWorkers {
		d.logger.Warn("Number of Container already reached the limit",
			"numberOfContainers", len(d.containers),
			"maxWorkers", d.maxWorkers)
	}
	d.mu.Unlock()

	cfg := &container.Config{
		Image: "songphuc/worker",
		Tty:   true,
	}

	hostCfg := &container.HostConfig{
		Resources: container.Resources{
			Memory:   d.memoryLimitMB * MB,
			NanoCPUs: d.cpunanoLimit,
		},
		NetworkMode: "none",
		RestartPolicy: container.RestartPolicy{
			Name:              "on-failure",
			MaximumRetryCount: 3,
		},
	}

	// create container
	resp, err := d.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		d.logger.Error("Failed to **create** Container",
			"err", err)
		return err
	}

	// start container
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		d.logger.Error("Failed to **start** Container",
			"container_id", resp.ID,
			"err", err)
		return err
	}

	// add to in-memory map
	d.mu.Lock()
	d.containers[resp.ID] = &ContainerInfo{
		ID:    resp.ID,
		State: StateIdle,
	}
	d.logger.Info("Container started",
		"container_id", resp.ID)
	d.mu.Unlock()

	return nil
}

// removeExcessContainer remove excess containers beyond maxWorkers
func (d *DockerContainerManager) removeExcessContainer(amount int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var removes []string
	for id := range d.containers {
		if len(removes) < amount {
			removes = append(removes, id)
		}
	}

	for _, id := range removes {
		if err := d.RemoveContainer(id); err != nil {
			return err
		}
	}

	return nil
}

// RemoveContainer safely remove a Container
func (d *DockerContainerManager) RemoveContainer(id string) error {
	ctx := context.Background()

	// remove container
	if err := d.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		d.logger.Error("Failed to **remove** container",
			"container_id", id,
			"err", err)
		return err
	}

	// remove container from in-memory map
	delete(d.containers, id)

	return nil
}

// MonitorContainers run in a loop to check containers health
func (d *DockerContainerManager) MonitorContainers(wg *sync.WaitGroup, intervalSecond int) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(intervalSecond) * time.Second)

	for range ticker.C {
		d.checkHealth()
	}
}

// checkHealth check the state of each Container
func (d *DockerContainerManager) checkHealth() {
	ctx := context.Background()
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		d.logger.Error("Failed to **list** containers",
			"err", err)
		return
	}

	runningWorkers := make(map[string]bool)
	for _, c := range containers {
		if d.isRunningContainer(c) {
			runningWorkers[c.ID] = true
		}
	}

	d.mu.Lock()
	for id := range d.containers {
		if !runningWorkers[id] {
			d.logger.Warn("Container not running, removing...",
				"container_id", id)

			if err := d.RemoveContainer(id); err != nil {
				d.logger.Error("Failed to **remove** container, continuing delete others...",
					"container_id", id)
				continue
			}
		}
	}
	d.mu.Unlock()
}

func (d *DockerContainerManager) isRunningContainer(c container.Summary) bool {
	_, exists := d.containers[c.ID]
	running := c.State != StateError
	return c.Image == "songphuc/worker" && exists && running
}

// balanceWorker ensure the number of workers is exactly equal to `maxWorkers`
func (d *DockerContainerManager) balanceWorker() error {
	currentCount := len(d.containers)
	if currentCount < d.maxWorkers {
		d.logger.Info("Current workers is not at the limit",
			"current", currentCount,
			"limit", d.maxWorkers)
		needed := d.maxWorkers - currentCount
		for range needed {
			if err := d.StartContainer(); err != nil {
				d.logger.Error("Failed to start Container",
					"err", err)
				return err
			}
		}
	} else if currentCount > d.maxWorkers {
		excess := currentCount - d.maxWorkers
		d.logger.Warn("Current workers is beyond the limit, removing...",
			"current", currentCount,
			"limit", d.maxWorkers)
		if err := d.removeExcessContainer(excess); err != nil {
			return err
		}
	}
	return nil
}

// GetAvailableContainer finds an Idle Container
func (d *DockerContainerManager) GetAvailableContainer() (string, error) {
	for range maxRetries {
		// Lock every trial
		d.mu.Lock()
		for id, info := range d.containers {
			if info.State == StateIdle {
				info.State = StateBusy
				d.mu.Unlock()
				d.logger.Info("Container is assigned to job",
					"container_id", id)
				return id, nil
			}
		}
		d.mu.Unlock()
		time.Sleep(time.Duration(retryDelayMS) * time.Millisecond)
	}

	return "", nil
}

// ShutDown cleans up all containers
func (d *DockerContainerManager) ShutDown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger.Info("Shutting down all Containers...")
	for _, c := range d.containers {
		d.RemoveContainer(c.ID)
	}
	d.logger.Info("Shutdown process is done")
}

func (d *DockerContainerManager) copyCodeToContainer(
	ctx context.Context,
	containerID, fileDir, fileName string,
	content []byte) error {

	var tarBuffer bytes.Buffer
	tw := tar.NewWriter(&tarBuffer)

	// Create a tar header for the file
	header := &tar.Header{
		Name: fileName,
		Mode: 0777, // File is executable
		Size: int64(len(content)),
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}

	tarReader := bytes.NewReader(tarBuffer.Bytes())

	// The destination path for CopyToContainer is the directory
	err := d.cli.CopyToContainer(ctx, containerID, fileDir, tarReader, container.CopyToContainerOptions{
		AllowOverwriteDirWithFile: true,
	})

	if err != nil {
		d.logger.Error("Failed to copy code to container", "container_id", containerID, "err", err)
		return err
	}

	return nil
}
