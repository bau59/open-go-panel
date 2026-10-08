package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"

	"github.com/creack/pty"
)

var usernameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type Session struct {
	ptmx *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func Start(ctx context.Context, username, workdir string, rows, cols uint16) (*Session, error) {
	if username != "root" && !usernameRE.MatchString(username) {
		return nil, errors.New("invalid terminal user")
	}
	if rows < 2 {
		rows = 24
	}
	if cols < 2 {
		cols = 100
	}

	var cmd *exec.Cmd
	if username == "root" {
		cmd = exec.CommandContext(ctx, "/bin/bash", "-l")
		cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	} else {
		cmd = exec.CommandContext(
			ctx,
			"runuser", "-u", username, "--",
			"env", "TERM=xterm-256color", "COLORTERM=truecolor",
			"/bin/bash", "-l",
		)
	}
	cmd.Dir = workdir

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, fmt.Errorf("start terminal for %s: %w", username, err)
	}
	return &Session{ptmx: ptmx, cmd: cmd}, nil
}

func (s *Session) Read(p []byte) (int, error) {
	if s == nil || s.ptmx == nil {
		return 0, io.EOF
	}
	return s.ptmx.Read(p)
}

func (s *Session) Write(p []byte) (int, error) {
	if s == nil || s.ptmx == nil {
		return 0, io.ErrClosedPipe
	}
	return s.ptmx.Write(p)
}

func (s *Session) Resize(rows, cols uint16) error {
	if s == nil || s.ptmx == nil {
		return io.ErrClosedPipe
	}
	if rows < 2 || cols < 2 {
		return nil
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.once.Do(func() {
		if s.ptmx != nil {
			closeErr = s.ptmx.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			_, _ = s.cmd.Process.Wait()
		}
	})
	return closeErr
}
