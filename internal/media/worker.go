package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const imageWorkerArg = "__elbot_media_worker"

type imageWorkerRequest struct {
	Data      string `json:"data"`
	MaxBytes  int64  `json:"max_bytes"`
	MaxLength int    `json:"max_length"`
}

type imageWorkerResponse struct {
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func init() {
	if len(os.Args) <= 1 || os.Args[1] != imageWorkerArg {
		return
	}
	if err := runImageWorker(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runImageWorker(stdin io.Reader, stdout io.Writer) error {
	var request imageWorkerRequest
	if err := json.NewDecoder(stdin).Decode(&request); err != nil {
		return fmt.Errorf("decode image worker request: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(request.Data)
	if err != nil {
		return fmt.Errorf("decode image worker data: %w", err)
	}
	compressed, err := compressImage(data, request.MaxBytes, request.MaxLength)
	response := imageWorkerResponse{}
	if err != nil {
		response.Error = err.Error()
	} else {
		response.Data = base64.StdEncoding.EncodeToString(compressed)
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		return fmt.Errorf("encode image worker response: %w", err)
	}
	return nil
}

func compressImageIsolated(ctx context.Context, data []byte, maxBytes int64, maxLength int) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable for image worker: %w", err)
	}
	payload, err := json.Marshal(imageWorkerRequest{
		Data:      base64.StdEncoding.EncodeToString(data),
		MaxBytes:  maxBytes,
		MaxLength: maxLength,
	})
	if err != nil {
		return nil, fmt.Errorf("encode image worker request: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, imageWorkerArg)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	configureWorkerProcess(cmd)
	cmd.Cancel = func() error {
		killWorkerProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return nil, runCtx.Err()
		}
		return nil, fmt.Errorf("image worker failed: %w: %s", err, truncateWorkerError(stderr.String()))
	}
	var response imageWorkerResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("decode image worker response: %w", err)
	}
	if response.Error != "" {
		return nil, fmt.Errorf("image worker: %s", response.Error)
	}
	compressed, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, fmt.Errorf("decode compressed image: %w", err)
	}
	return compressed, nil
}

func truncateWorkerError(text string) string {
	if len(text) <= 512 {
		return text
	}
	return text[:512] + "..."
}
