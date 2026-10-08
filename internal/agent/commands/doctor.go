package commands

import (
	"context"

	"elbot/internal/command"
)

// DoctorService runs the read-only configuration check shown by /doctor.
type DoctorService interface {
	Doctor(ctx context.Context) (string, error)
}

// DoctorModule registers /doctor.
type DoctorModule struct{}

func (DoctorModule) RegisterCommands(registrar Registrar, deps Deps) error {
	return RegisterFactories(registrar, deps, NewDoctor)
}

// NewDoctor exposes the read-only configuration check. It never edits files and
// is superadmin-only (Info.MinRole stays empty, which the router treats as
// superadmin).
func NewDoctor(deps Deps) command.Handler {
	return command.NewFunc(command.Info{
		Name:        "doctor",
		Usage:       "/doctor",
		Description: "Read-only configuration check: reports missing items, TOML errors, unknown keys and built-in Skill differences. Only superadmins can use it; nothing is modified.",
	}, func(ctx context.Context, _ command.Request) (*command.Result, error) {
		if deps.Doctor == nil {
			return &command.Result{Content: "配置检查未启用。"}, nil
		}
		text, err := deps.Doctor.Doctor(ctx)
		if err != nil {
			return nil, err
		}
		return &command.Result{Content: text}, nil
	})
}
