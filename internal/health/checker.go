package health

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Checker is an external readiness check evaluated by the HTTP server.
type Checker interface {
	Name() string
	Check(context.Context) error
}

// CheckerFunc adapts a function to a Checker.
type CheckerFunc struct {
	CheckName string
	Fn        func(context.Context) error
}

func (c CheckerFunc) Name() string {
	if strings.TrimSpace(c.CheckName) == "" {
		return "check"
	}
	return c.CheckName
}

func (c CheckerFunc) Check(ctx context.Context) error {
	if c.Fn == nil {
		return nil
	}
	return c.Fn(ctx)
}

// DirWritable verifies that a directory can create and remove a temporary file.
type DirWritable struct {
	CheckName string
	Dir       string
}

func (c DirWritable) Name() string {
	if strings.TrimSpace(c.CheckName) == "" {
		return "dir_writable"
	}
	return c.CheckName
}

func (c DirWritable) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := strings.TrimSpace(c.Dir)
	if dir == "" {
		return fmt.Errorf("directory is empty")
	}
	f, err := os.CreateTemp(dir, ".elbot-health-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.WriteString("ok\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	return nil
}
