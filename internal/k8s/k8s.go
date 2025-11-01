package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"time"

	batchv1 "k8s.io/api/batch/v1" // <-- ADDED IMPORT
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type K8sClient struct {
	Clientset *kubernetes.Clientset
}

func GetK8sClient() (*K8sClient, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first (for running inside Kubernetes)
	config, err = rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig file (for local development)
		var kubeconfig string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		} else {
			return nil, fmt.Errorf("home directory not found and in-cluster config failed: %v", err)
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from flags: %v", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %v", err)
	}
	return &K8sClient{
		Clientset: clientset,
	}, nil
}

// CreateJobParams holds the configuration for a new Kubernetes Job
type CreateJobParams struct {
	BaseName string // Base name for the job. A unique suffix will be added.

	Image string // The container image to run

	Namespace string // Namespace to run the job in

	Command []string // Command to execute in the container (overrides ENTRYPOINT)

	Args []string // Arguments to pass to the command (overrides CMD)

	EnvVars map[string]string // Environment variables to set in the container
}

// CreateJob creates a new K8s Job based on the provided parameters.
// It returns the unique name of the created job and an error, if any.
func (cli *K8sClient) CreateJob(params CreateJobParams) (string, error) {
	// 1. Convert EnvVars map to a slice of corev1.EnvVar
	var envs []corev1.EnvVar
	for k, v := range params.EnvVars {
		envs = append(envs, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	// 2. Define the Job spec
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// Use GenerateName to ensure a unique job name
			GenerateName: params.BaseName,
			Namespace:    params.Namespace,
		},
		Spec: batchv1.JobSpec{
			// Time to live after finishing (cleans up the job object)
			TTLSecondsAfterFinished: int32Ptr(300), // 5 minutes
			// Number of retries before marking as failed
			BackoffLimit: int32Ptr(1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "executor-container",
							Image:           params.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         params.Command,
							Args:            params.Args,
							Env:             envs,
						},
					},
					// Do not restart pods, the Job controller handles retries
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	// 3. Create the Job
	log.Printf("Creating job %s in namespace %s...", params.BaseName, params.Namespace)
	createdJob, err := cli.Clientset.BatchV1().Jobs(params.Namespace).Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create job: %v", err)
	}

	log.Printf("Job '%s' created successfully.", createdJob.Name)
	// Return the actual name (BaseName + random suffix)
	return createdJob.Name, nil
}

// DeleteJob deletes a job and its associated pods.
func (cli *K8sClient) DeleteJob(namespace, jobName string) error {
	log.Printf("Deleting job '%s'...", jobName)

	// Set the propagation policy to delete dependent pods
	deletePolicy := metav1.DeletePropagationBackground

	err := cli.Clientset.BatchV1().Jobs(namespace).Delete(context.TODO(), jobName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})

	if err != nil {
		return fmt.Errorf("failed to delete job: %v", err)
	}

	log.Printf("Job '%s' deleted.", jobName)
	return nil
}

// WaitForJobCompletion polls the job until it's complete or failed
func (cli *K8sClient) WaitForJobCompletion(namespace, jobName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		job, err := cli.Clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get job status: %v", err)
		}

		if job.Status.Succeeded > 0 {
			return nil
		}

		if job.Status.Failed > 0 {
			return fmt.Errorf("job failed (failed pods: %d)", job.Status.Failed)
		}

		// Job is still running
		log.Printf("Job '%s' is still running (Active: %d, Succeeded: %d, Failed: %d)...",
			job.Name, job.Status.Active, job.Status.Succeeded, job.Status.Failed)

		// Wait before polling again
		select {
		case <-time.After(500 * time.Millisecond):
			// Continue loop
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for job to complete")
		}
	}
}

// GetJobLogs finds the pod associated with a job and streams its logs
// MODIFIED: This function now returns the logs as a string
func (cli *K8sClient) GetJobLogs(namespace, jobName string) (string, error) {
	// A job creates pods with a "job-name" label
	labelSelector := fmt.Sprintf("job-name=%s", jobName)

	// 1. Find the pod(s) for this job
	podList, err := cli.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for job: %v", err)
	}
	if len(podList.Items) == 0 {
		return "", fmt.Errorf("no pods found for job '%s'", jobName)
	}

	// We'll just get logs from the first pod found
	podName := podList.Items[0].Name

	// 2. Stream the logs from that pod
	req := cli.Clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		// Follow: false, // We don't need to follow, the job is already complete
	})

	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", fmt.Errorf("failed to stream logs: %v", err)
	}
	defer podLogs.Close()

	// Copy the log stream to a bytes.Buffer
	var buf bytes.Buffer
	_, err = io.Copy(&buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("failed to copy log stream: %v", err)
	}

	// Return the buffer's content as a string
	return buf.String(), nil
}

func int32Ptr(i int32) *int32 { return &i }
