package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/k8s"
)

const (
	CodeRunTimeOutSecond = 15 * time.Second
)

type CodeErr string

const (
	CompileError      CodeErr = "COMPILE_ERROR"
	RunTimeError      CodeErr = "RUNTIME_ERROR"
	FailTestCase      CodeErr = "WRONG_ANSWER"
	TimeLimitExceeded CodeErr = "TIME_LIMIT_EXCEEDED"
)

type TestCase struct {
	Input         string
	ExpectedOuput string
}

type ExecutionJob struct {
	Language     string
	Code         string
	DriverCode   string
	CompileCmd   string
	RunCmd       string
	TempFileDir  string
	TempFileName string
	TestCases    []TestCase
}

type ExecutionResult struct {
	Stdout        string  `json:"stdout"`
	Stderr        string  `json:"stderr"`
	Message       string  `json:"message"`
	Success       bool    `json:"success"`
	Error         CodeErr `json:"error"`
	ExecutionTime string  `json:"execution_time_ms"`
}

type Executor struct {
	logger      *slog.Logger
	k8sClient   *k8s.K8sClient
	codeBuilder CodeBuilder
}

const runnerScriptTemplate = `
set -e

# 1. Write the code from $USER_CODE env var
echo "Writing code to file..."
mkdir -p {{temp_file_dir}}
echo "$USER_CODE" > {{full_file_path}}

# 2. Compile (if compile_cmd is provided)
{{compile_cmd}}

# 3. Save test cases from env vars to temp files
echo "$TC_INPUTS" > /tmp/inputs
echo "$TC_EXPECTED" > /tmp/expected

total_time=0
test_case_index=0

# 4. Use 'paste' to read inputs/expected line-by-line
paste /tmp/inputs /tmp/expected | while read -r input_b64 expected_b64; do
    if [ -z "$input_b64" ]; then
        continue # Skip empty lines
    fi

    start_time=$(python3 -c "import time; print(int(time.time()*1000))")

    # Decode input, run command, get actual output
    actual_output=$(echo "$input_b64" | base64 -d | {{run_cmd}})

    end_time=$(python3 -c "import time; print(int(time.time()*1000))")
    exec_time=$((end_time - start_time))
    total_time=$((total_time + exec_time))

    # Decode expected output
    expected_output=$(echo "$expected_b64" | base64 -d)

    # Trim whitespace for comparison. 'xargs' is a portable way to do this.
    actual_output_trimmed=$(echo "$actual_output" | xargs)
    expected_output_trimmed=$(echo "$expected_output" | xargs)

    # 5. Compare
    if [ "$actual_output_trimmed" != "$expected_output_trimmed" ]; then
        echo "---R_L_ERROR_START---"
        echo "Wrong Answer on Test Case $test_case_index"
        echo "Input (base64): $input_b64"
        echo "Expected (base64): $expected_b64"
        echo "Actual (raw): $actual_output"
        echo "---R_L_ERROR_END---"
        exit 1 # Fail the job
    fi
    test_case_index=$((test_case_index + 1))
done

# 6. If loop finishes, all test cases passed
echo "---R_L_SUCCESS_START---"
echo "All $test_case_index test cases passed!"
echo "$total_time"
echo "---R_L_SUCCESS_END---"
`

func NewExecutorEngine(logger *slog.Logger, k8sCli *k8s.K8sClient, cb CodeBuilder) *Executor {
	return &Executor{
		logger:      logger,
		k8sClient:   k8sCli,
		codeBuilder: cb,
	}
}

func (e *Executor) Execute(ctx context.Context, job ExecutionJob) ExecutionResult {
	// 1. Build the complete code. If this fails, it's a "compile" error.
	finalCode, err := e.codeBuilder.Build(job.Language, job.DriverCode, job.Code)
	if err != nil {
		return ExecutionResult{Success: false, Message: err.Error(), Error: CompileError}
	}

	// 2. Prepare the runner script by replacing placeholders
	script := strings.ReplaceAll(runnerScriptTemplate, "{{temp_file_dir}}", job.TempFileDir)
	script = strings.ReplaceAll(script, "{{temp_file_name}}", job.TempFileName)
	fullFilePath := filepath.Join(job.TempFileDir, job.TempFileName)
	script = strings.ReplaceAll(script, "{{full_file_path}}", fullFilePath)

	// Replace compile command (or remove if empty)
	if job.CompileCmd != "" {
		compileCmd := strings.ReplaceAll(job.CompileCmd, tempFileDirHolder, job.TempFileDir)
		script = strings.ReplaceAll(script, "{{compile_cmd}}", compileCmd)
	} else {
		script = strings.ReplaceAll(script, "{{compile_cmd}}", "echo 'No compilation needed'")
	}

	// Replace run command
	runCmd := strings.ReplaceAll(job.RunCmd, tempFileDirHolder, job.TempFileDir)
	script = strings.ReplaceAll(script, "{{run_cmd}}", runCmd)

	// 3. Encode all test case inputs and expected outputs as base64 strings
	var inputsBuilder strings.Builder
	var expectedBuilder strings.Builder

	for i, tc := range job.TestCases {
		if i > 0 {
			inputsBuilder.WriteString("\n")
			expectedBuilder.WriteString("\n")
		}
		// Base64 encode to handle special characters and newlines
		inputsBuilder.WriteString(encodeBase64(tc.Input))
		expectedBuilder.WriteString(encodeBase64(tc.ExpectedOuput))
	}

	namespace := "default" // Or get from config

	// 4. Create and run a single K8s Job for all test cases
	start := time.Now()
	e.logger.Info("Starting K8s job for all test cases", "test_case_count", len(job.TestCases))

	params := k8s.CreateJobParams{
		BaseName:  "executor-job-",
		Namespace: namespace,
		Image:     "songphuc/worker:latest",
		Command:   []string{"/bin/sh", "-c"},
		Args:      []string{script},
		EnvVars: map[string]string{
			"USER_CODE":   finalCode,
			"TC_INPUTS":   inputsBuilder.String(),
			"TC_EXPECTED": expectedBuilder.String(),
		},
	}

	// Create job
	jobName, err := e.k8sClient.CreateJob(params)
	if err != nil {
		e.logger.Error("failed to create k8s job", "error", err)
		return ExecutionResult{Success: false, Message: "Failed to create execution job."}
	}

	// Put job deletion to another goroutine
	go func(name string) {
		time.Sleep(30 * time.Second) // Wait before cleanup
		if err := e.k8sClient.DeleteJob(namespace, name); err != nil {
			e.logger.Warn("failed to delete k8s job", "jobName", name, "error", err)
		}
	}(jobName)

	// Wait for job to complete
	err = e.k8sClient.WaitForJobCompletion(namespace, jobName, CodeRunTimeOutSecond)

	// Get logs *regardless* of completion error, as they are useful for debugging
	stdout, logErr := e.k8sClient.GetJobLogs(namespace, jobName)
	if logErr != nil {
		e.logger.Error("failed to get job logs", "jobName", jobName, "error", logErr)
	}

	// Track total wall-clock time
	duration := time.Since(start)
	wallClockTime := duration.Milliseconds()

	// 5. Parse the output to determine success or failure
	return e.parseJobOutput(stdout, wallClockTime, err, job)
}

// parseJobOutput analyzes the job logs to determine the execution result
func (e *Executor) parseJobOutput(logs string, wallClockTime int64, jobErr error, job ExecutionJob) ExecutionResult {
	// Check for timeout
	if jobErr != nil && strings.Contains(jobErr.Error(), "timeout") {
		return ExecutionResult{
			Success: false,
			Stdout:  cleanStdout(logs),
			Stderr:  cleanStdout(logs),
			Message: "Execution timed out",
			Error:   TimeLimitExceeded,
		}
	}

	// Look for our custom success marker
	if strings.Contains(logs, "---R_L_SUCCESS_START---") && strings.Contains(logs, "---R_L_SUCCESS_END---") {
		// Extract execution time from logs
		successStart := strings.Index(logs, "---R_L_SUCCESS_START---")
		successEnd := strings.Index(logs, "---R_L_SUCCESS_END---")
		successSection := logs[successStart:successEnd]

		// Parse the execution time (it's the last number before the end marker)
		lines := strings.Split(successSection, "\n")
		executionTime := fmt.Sprintf("%d", wallClockTime)
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && isNumeric(line) {
				executionTime = line
			}
		}

		// Extract the actual test case count from the success message
		testCaseCount := len(job.TestCases)
		message := fmt.Sprintf("All %d test cases passed!", testCaseCount)

		return ExecutionResult{
			Success:       true,
			Stdout:        "", // No stdout for successful test runs
			Message:       message,
			ExecutionTime: executionTime,
		}
	}

	// Look for our custom error marker (wrong answer)
	if strings.Contains(logs, "---R_L_ERROR_START---") && strings.Contains(logs, "---R_L_ERROR_END---") {
		errorStart := strings.Index(logs, "---R_L_ERROR_START---")
		errorEnd := strings.Index(logs, "---R_L_ERROR_END---")
		errorSection := logs[errorStart+len("---R_L_ERROR_START---") : errorEnd]

		// Parse the error section to extract meaningful information
		message := parseWrongAnswerMessage(errorSection)

		return ExecutionResult{
			Success: false,
			Stdout:  cleanStdout(logs),
			Stderr:  cleanStdout(logs),
			Message: message,
			Error:   FailTestCase,
		}
	}

	// If job failed but no custom markers, it's likely a compile or runtime error
	if jobErr != nil {
		errType := RunTimeError
		if job.CompileCmd != "" && strings.Contains(logs, "error:") {
			errType = CompileError
		}

		return ExecutionResult{
			Success: false,
			Stdout:  cleanStdout(logs),
			Stderr:  cleanStdout(logs),
			Message: fmt.Sprintf("Execution failed: %v", jobErr),
			Error:   errType,
		}
	}

	// Unexpected state
	return ExecutionResult{
		Success: false,
		Stdout:  cleanStdout(logs),
		Stderr:  cleanStdout(logs),
		Message: "Unexpected execution state",
		Error:   RunTimeError,
	}
}

// cleanStdout removes internal script messages and delimiters from logs
func cleanStdout(logs string) string {
	// Remove internal markers
	cleaned := logs

	// Remove success markers
	cleaned = removeSection(cleaned, "---R_L_SUCCESS_START---", "---R_L_SUCCESS_END---")

	// Remove error markers
	cleaned = removeSection(cleaned, "---R_L_ERROR_START---", "---R_L_ERROR_END---")

	// Remove common script output messages
	linesToRemove := []string{
		"Writing code to file...",
		"No compilation needed",
	}

	lines := strings.Split(cleaned, "\n")
	var filteredLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		shouldKeep := true

		for _, remove := range linesToRemove {
			if trimmed == remove {
				shouldKeep = false
				break
			}
		}

		if shouldKeep && trimmed != "" {
			filteredLines = append(filteredLines, line)
		}
	}

	return strings.TrimSpace(strings.Join(filteredLines, "\n"))
}

// removeSection removes a section of text between two markers (inclusive)
func removeSection(text, startMarker, endMarker string) string {
	if !strings.Contains(text, startMarker) || !strings.Contains(text, endMarker) {
		return text
	}

	startIdx := strings.Index(text, startMarker)
	endIdx := strings.Index(text, endMarker)

	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return text
	}

	// Remove from start marker to end marker (inclusive)
	return text[:startIdx] + text[endIdx+len(endMarker):]
}

// parseWrongAnswerMessage formats the wrong answer error message in a user-friendly way
func parseWrongAnswerMessage(errorSection string) string {
	lines := strings.Split(strings.TrimSpace(errorSection), "\n")

	var testCaseNum string
	var inputB64 string
	var expectedB64 string
	var actualOutput string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Wrong Answer on Test Case") {
			parts := strings.Split(line, "Test Case ")
			if len(parts) > 1 {
				testCaseNum = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(line, "Input (base64):") {
			inputB64 = strings.TrimSpace(strings.TrimPrefix(line, "Input (base64):"))
		} else if strings.HasPrefix(line, "Expected (base64):") {
			expectedB64 = strings.TrimSpace(strings.TrimPrefix(line, "Expected (base64):"))
		} else if strings.HasPrefix(line, "Actual (raw):") {
			actualOutput = strings.TrimSpace(strings.TrimPrefix(line, "Actual (raw):"))
		}
	}

	// Decode base64 values
	input := decodeBase64(inputB64)
	expected := decodeBase64(expectedB64)

	// Build user-friendly message
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Wrong Answer on Test Case %s\n\n", testCaseNum))
	msg.WriteString(fmt.Sprintf("Input:\n%s\n\n", input))
	msg.WriteString(fmt.Sprintf("Expected Output:\n%s\n\n", expected))
	msg.WriteString(fmt.Sprintf("Your Output:\n%s", actualOutput))

	return msg.String()
}

// decodeBase64 safely decodes a base64 string, returning the original if decode fails
func decodeBase64(encoded string) string {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return encoded // Return original if decode fails
	}
	return string(decoded)
}

// encodeBase64 encodes a string to base64
func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// isNumeric checks if a string contains only digits
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
