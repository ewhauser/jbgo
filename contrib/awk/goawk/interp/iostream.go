package interp

// I/O streams are interfaces which allow file redirects and command pipelines to be treated
// equivalently.

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"syscall"
)

const (
	notClosedExitCode = -127
)

var (
	errDoubleClose = errors.New("close: stream already closed")
)

// firstError returns the first non-nil error or nil if all errors are nil.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

type waitFunc func() (int, error)

// Close the cmd and convert the error result into the result returned from goawk builtin functions.
// A nil error is returned if that error describes a non-zero exit status or an unhandled signal.
// Any other type of error returns -1 and err.
//
// The result mimicks gawk for expected child process errors:
// 1. Returns the exit status of the child process and nil error on normal process exit.
// 2. Returns 256 + signal on unhandled signal exit.
// 3. Returns 512 + signal on unhandled signal exit which caused a core dump.
func waitExitCodeCmd(cmd *exec.Cmd) (int, error) {
	err := cmd.Wait()
	if err == nil {
		return 0, nil
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		// Wait() returned an io error.
		return -1, err
	}
	status, ok := ee.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		// Maybe not all platforms support WaitStatus?
		return -1, err
	}
	switch {
	case status.CoreDump():
		return 512 + int(status.Signal()), nil
	case status.Signaled():
		return 256 + int(status.Signal()), nil
	case status.Exited():
		return status.ExitStatus(), nil
	default:
		return -1, err
	}
}

type inputStream interface {
	io.ReadCloser
	ExitCode() int
}

type outputStream interface {
	io.WriteCloser
	Flush() error
	ExitCode() int
}

type outFileStream struct {
	*bufio.Writer
	closer   io.Closer
	exitCode int
	closed   bool
}

func newOutFileStream(wc io.WriteCloser, size int) outputStream {
	b := bufio.NewWriterSize(wc, size)
	return &outFileStream{b, wc, notClosedExitCode, false}
}

func (s *outFileStream) Close() error {
	if s.closed {
		return errDoubleClose
	}
	s.closed = true
	flushErr := s.Writer.Flush()
	closeErr := s.closer.Close()
	if err := firstError(flushErr, closeErr); err != nil {
		s.exitCode = -1
		return err
	}
	s.exitCode = 0
	return nil
}

func (s *outFileStream) ExitCode() int {
	return s.exitCode
}

type outCmdStream struct {
	*bufio.Writer
	closer   io.Closer
	wait     waitFunc
	exitCode int
	closed   bool
}

func newOutCmdStream(w io.WriteCloser, wait waitFunc) outputStream {
	return &outCmdStream{bufio.NewWriterSize(w, outputBufSize), w, wait, notClosedExitCode, false}
}

func newOutExecCmdStream(cmd *exec.Cmd) (outputStream, error) {
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, newError("error connecting to stdin pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	return newOutCmdStream(w, func() (int, error) {
		return waitExitCodeCmd(cmd)
	}), nil
}

func (s *outCmdStream) Close() error {
	if s.closed {
		return errDoubleClose
	}
	s.closed = true
	flushErr := s.Writer.Flush()
	closeErr := s.closer.Close()
	waitErr := error(nil)
	if s.wait != nil {
		s.exitCode, waitErr = s.wait()
	} else {
		s.exitCode = 0
	}
	return firstError(waitErr, flushErr, closeErr)
}

func (s *outCmdStream) ExitCode() int {
	return s.exitCode
}

// An outNullStream allows writes to not do anything while fulfilling the outputStream interface.
type outNullStream struct {
	io.Writer
	closed bool
}

func newOutNullStream() outputStream { return &outNullStream{io.Discard, false} }
func (s outNullStream) Flush() error { return nil }
func (s *outNullStream) Close() error {
	if s.closed {
		return errDoubleClose
	}
	s.closed = true
	return nil
}
func (s outNullStream) ExitCode() int { return -1 }

type inFileStream struct {
	io.ReadCloser
	exitCode int
	closed   bool
}

func newInFileStream(rc io.ReadCloser) inputStream {
	return &inFileStream{rc, notClosedExitCode, false}
}

func (s *inFileStream) Close() error {
	if s.closed {
		return errDoubleClose
	}
	s.closed = true
	if err := s.ReadCloser.Close(); err != nil {
		s.exitCode = -1
		return err
	}
	s.exitCode = 0
	return nil
}

func (s *inFileStream) ExitCode() int {
	return s.exitCode
}

type inCmdStream struct {
	io.ReadCloser
	wait     waitFunc
	exitCode int
	closed   bool
}

func newInCmdStream(r io.ReadCloser, wait waitFunc) inputStream {
	return &inCmdStream{r, wait, notClosedExitCode, false}
}

func newInExecCmdStream(cmd *exec.Cmd) (inputStream, error) {
	r, err := cmd.StdoutPipe()
	if err != nil {
		return nil, newError("error connecting to stdout pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	return newInCmdStream(r, func() (int, error) {
		return waitExitCodeCmd(cmd)
	}), nil
}

func (s *inCmdStream) Close() error {
	if s.closed {
		return errDoubleClose
	}
	s.closed = true
	closeErr := s.ReadCloser.Close()
	waitErr := error(nil)
	if s.wait != nil {
		s.exitCode, waitErr = s.wait()
	} else {
		s.exitCode = 0
	}
	return firstError(waitErr, closeErr)
}

func (s *inCmdStream) ExitCode() int {
	return s.exitCode
}
