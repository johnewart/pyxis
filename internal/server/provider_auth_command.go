package server

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/creack/pty"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// providerAuthCommandStream is the subset of grpc.BidiStreamingServer /
// connect.BidiStream that RunProviderAuthCommand needs, letting the same
// implementation back both the native gRPC and Connect adapters.
type providerAuthCommandStream interface {
	Send(*llmv1.ProviderAuthCommandOutput) error
	Receive() (*llmv1.ProviderAuthCommandInput, error)
}

// grpcProviderAuthCommandStream adapts grpc.BidiStreamingServer's Recv/Send to
// the Send/Receive shape above (connect.BidiStream already matches it).
type grpcProviderAuthCommandStream struct {
	llmv1.LlmService_RunProviderAuthCommandServer
}

func (s *grpcProviderAuthCommandStream) Receive() (*llmv1.ProviderAuthCommandInput, error) {
	return s.Recv()
}

// resolveProviderAuthBinary picks the CLI binary for an interactive auth
// command: explicit override > env var > PATH lookup of the default name.
func resolveProviderAuthBinary(override, envVar, defaultName string) (string, error) {
	b := strings.TrimSpace(override)
	if b == "" {
		b = strings.TrimSpace(os.Getenv(envVar))
	}
	if b == "" {
		b = defaultName
	}
	if filepath.Base(b) == b {
		p, err := exec.LookPath(b)
		if err != nil {
			return "", fmt.Errorf("look up %q on PATH: %w", b, err)
		}
		return p, nil
	}
	return filepath.Abs(b)
}

// providerAuthCommandSpec is what a provider type needs to run its
// interactive auth command: a binary name/env var to resolve and the args
// that start the login flow.
type providerAuthCommandSpec struct {
	envVar      string
	defaultName string
	args        []string
}

func providerAuthCommandSpecFor(t llmv1.LlmProviderType) (providerAuthCommandSpec, error) {
	switch t {
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE, llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2:
		return providerAuthCommandSpec{envVar: "CLAUDE_CLI_PATH", defaultName: "claude", args: []string{"login"}}, nil
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_UNSPECIFIED:
		// Report the missing field, not the zero value. "interactive auth is not
		// supported for provider type LLM_PROVIDER_TYPE_UNSPECIFIED" reads as
		// gibberish to whoever hit it: nothing asked for UNSPECIFIED, the field
		// just never arrived (a client mapping an unknown provider name to the
		// enum default is the usual cause).
		return providerAuthCommandSpec{}, errors.New(
			"provider_type is required and was not set (got LLM_PROVIDER_TYPE_UNSPECIFIED); " +
				"pass the provider type to authenticate, e.g. LLM_PROVIDER_TYPE_CLAUDE_CODE_V2")
	default:
		return providerAuthCommandSpec{}, fmt.Errorf("interactive auth is not supported for provider type %s", t)
	}
}

// RunProviderAuthCommand runs an interactive CLI auth command (e.g. `claude
// login`) attached to a server-side PTY and pipes its I/O over the stream.
// See ProviderAuthCommandStart's proto doc comment for scope/rationale.
func (s *LlmServer) RunProviderAuthCommand(stream llmv1.LlmService_RunProviderAuthCommandServer) error {
	return s.runProviderAuthCommand(&grpcProviderAuthCommandStream{stream})
}

func (s *LlmServer) runProviderAuthCommand(stream providerAuthCommandStream) error {
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return errors.New("RunProviderAuthCommand: first frame must be `start`")
	}

	spec, err := providerAuthCommandSpecFor(start.GetProviderType())
	if err != nil {
		return err
	}
	binary, err := resolveProviderAuthBinary(start.GetBinary(), spec.envVar, spec.defaultName)
	if err != nil {
		return fmt.Errorf("resolve auth command binary: %w", err)
	}

	cmd := exec.Command(binary, spec.args...)
	if cd := strings.TrimSpace(start.GetConfigDir()); cd != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+cd)
	}

	cols, rows := start.GetCols(), start.GetRows()
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return fmt.Errorf("start %s in pty: %w", binary, err)
	}
	defer ptyFile.Close()

	if err := stream.Send(&llmv1.ProviderAuthCommandOutput{
		Frame: &llmv1.ProviderAuthCommandOutput_Started{Started: true},
	}); err != nil {
		return err
	}

	var sendMu sync.Mutex
	safeSend := func(out *llmv1.ProviderAuthCommandOutput) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(out)
	}

	// Pump PTY output -> stream, until EOF (process exited) or a read error.
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptyFile.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				if sendErr := safeSend(&llmv1.ProviderAuthCommandOutput{
					Frame: &llmv1.ProviderAuthCommandOutput_Output{Output: chunk},
				}); sendErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Pump stream input -> PTY (stdin writes, resizes) until the client
	// closes the send side or the process has exited.
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		for {
			in, recvErr := stream.Receive()
			if recvErr != nil {
				return
			}
			switch frame := in.GetFrame().(type) {
			case *llmv1.ProviderAuthCommandInput_Stdin:
				if _, writeErr := ptyFile.Write(frame.Stdin); writeErr != nil {
					return
				}
			case *llmv1.ProviderAuthCommandInput_Resize:
				_ = pty.Setsize(ptyFile, &pty.Winsize{
					Cols: uint16(frame.Resize.GetCols()),
					Rows: uint16(frame.Resize.GetRows()),
				})
			}
		}
	}()

	waitErr := cmd.Wait()
	_ = ptyFile.Close()
	<-outputDone
	// The input pump exits on its own once Receive() errors (client closed
	// send side, or the underlying connection tears down with the process);
	// don't block RunProviderAuthCommand's return on it indefinitely.
	select {
	case <-inputDone:
	default:
	}

	code := 0
	var exitErr *exec.ExitError
	if waitErr != nil {
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		} else if !errors.Is(waitErr, io.EOF) {
			code = -1
		}
	}
	return safeSend(&llmv1.ProviderAuthCommandOutput{
		Frame: &llmv1.ProviderAuthCommandOutput_ExitCode{ExitCode: int32(code)},
	})
}
