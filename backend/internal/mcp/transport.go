package mcp

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"syscall"
)

// Transport is one MCP server connection. WriteLine sends a JSON-RPC message
// (a single line); ReadLine returns one response line. The seam lets tests
// substitute an in-memory fake for the real stdio child process.
type Transport interface {
	WriteLine(p []byte) error
	ReadLine() ([]byte, error) // io.EOF when the server closed
	Stop() error
}

// TransportFactory builds a Transport for a configured server.
type TransportFactory interface {
	New(ctx context.Context, spec ServerSpec) (Transport, error)
}

// StdioTransport runs one MCP server as a child process in its own process
// group so Stop() can kill the whole group.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func NewStdioTransport(ctx context.Context, command string, args []string) (*StdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &StdioTransport{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}, nil
}

func (t *StdioTransport) WriteLine(p []byte) error {
	if _, err := t.stdin.Write(p); err != nil {
		return err
	}
	_, err := t.stdin.Write([]byte("\n"))
	return err
}

func (t *StdioTransport) ReadLine() ([]byte, error) {
	return t.stdout.ReadBytes('\n')
}

func (t *StdioTransport) Stop() error {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
		_, _ = t.cmd.Process.Wait()
	}
	return nil
}

// StdioTransportFactory is the production TransportFactory.
type StdioTransportFactory struct{}

func NewStdioTransportFactory() *StdioTransportFactory { return &StdioTransportFactory{} }

func (f *StdioTransportFactory) New(ctx context.Context, spec ServerSpec) (Transport, error) {
	return NewStdioTransport(ctx, spec.Command, spec.Args)
}

// SpecTransportFactory dispatches on spec.Type: http → remote HTTP transport,
// otherwise stdio child process.
type SpecTransportFactory struct {
	HTTP *HTTPTransportFactory
}

func NewSpecTransportFactory() *SpecTransportFactory {
	return &SpecTransportFactory{HTTP: NewHTTPTransportFactory()}
}

func (f *SpecTransportFactory) New(ctx context.Context, spec ServerSpec) (Transport, error) {
	if spec.Type == "http" {
		return f.HTTP.New(ctx, spec)
	}
	return NewStdioTransport(ctx, spec.Command, spec.Args)
}
