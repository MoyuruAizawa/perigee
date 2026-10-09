package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"perigee/internal/config"
	"perigee/internal/container"
)

type Runtime interface {
	List(context.Context) ([]container.Snapshot, error)
	Run(context.Context, config.Config, int, string, string) error
	Stop(context.Context, string) error
	Remove(context.Context, string) error
}
type GitHub interface {
	RegistrationToken(context.Context) (string, error)
	DeleteOwned(context.Context, string, map[string]bool, map[string]bool) error
}
type Manager struct {
	Config    config.Config
	Runtime   Runtime
	GitHub    GitHub
	Log       io.Writer
	recovered bool
}

func (m *Manager) log(format string, args ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, format+"\n", args...)
	}
}

func (m *Manager) Reconcile(ctx context.Context) error {
	items, err := m.Runtime.List(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	byID := map[string]container.Snapshot{}
	keep := map[string]bool{}
	for _, s := range items {
		byID[s.ID] = s
		if s.Owned(m.Config.Owner()) && s.Status.State != "stopped" {
			if s.RunnerName() == "" {
				return fmt.Errorf("managed container %s lacks runner label", s.ID)
			}
			keep[s.RunnerName()] = true
		}
	}
	// Fail before any mutation when a desired slot is occupied by another owner.
	for slot := 0; slot < m.Config.Runner.Count; slot++ {
		if s, ok := byID[m.Config.ContainerName(slot)]; ok && !s.Owned(m.Config.Owner()) {
			return fmt.Errorf("container %s is not owned by this Perigee configuration", s.ID)
		}
	}
	var failures []error
	if !m.recovered {
		if err := m.GitHub.DeleteOwned(ctx, m.Config.OwnerLabel(), nil, keep); err != nil {
			failures = append(failures, fmt.Errorf("recover GitHub registrations: %w", err))
		} else {
			m.recovered = true
		}
	}
	// Reconcile count reductions, including runners left by a previous process.
	wanted := map[string]bool{}
	for slot := 0; slot < m.Config.Runner.Count; slot++ {
		wanted[m.Config.ContainerName(slot)] = true
	}
	for _, s := range items {
		if !s.Owned(m.Config.Owner()) || wanted[s.ID] {
			continue
		}
		if err := m.remove(ctx, s); err != nil {
			failures = append(failures, err)
		}
	}
	for slot := 0; slot < m.Config.Runner.Count; slot++ {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		id := m.Config.ContainerName(slot)
		if s, ok := byID[id]; ok {
			switch s.Status.State {
			case "running", "stopping":
				continue
			case "stopped":
				if err := m.remove(ctx, s); err != nil {
					failures = append(failures, err)
					continue
				}
			default:
				failures = append(failures, fmt.Errorf("container %s has unknown state; leaving untouched", id))
				continue
			}
		}
		token, err := m.GitHub.RegistrationToken(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("slot %d registration: %w", slot+1, err))
			continue
		}
		// Unique names avoid --replace overwriting an unrelated GitHub runner.
		name := fmt.Sprintf("%s-%d-%s", m.Config.Runner.NamePrefix, slot+1, rand.Text()[:10])
		if err := m.Runtime.Run(ctx, m.Config, slot, name, token); err != nil {
			// A failed launch may have created a container/registration. Recover on
			// the next successful list, without guessing about its runtime state.
			m.recovered = false
			failures = append(failures, fmt.Errorf("slot %d launch: %w", slot+1, err))
			continue
		}
		m.log("started %s (%s)", id, name)
	}
	return errors.Join(failures...)
}
func (m *Manager) remove(ctx context.Context, s container.Snapshot) error {
	if !s.Owned(m.Config.Owner()) {
		return fmt.Errorf("refusing to remove unowned container %s", s.ID)
	}
	if s.Status.State == "unknown" {
		return fmt.Errorf("refusing to remove container %s with unknown state", s.ID)
	}
	if s.Status.State != "stopped" {
		if err := m.Runtime.Stop(ctx, s.ID); err != nil {
			return fmt.Errorf("stop %s: %w", s.ID, err)
		}
	}
	if err := m.Runtime.Remove(ctx, s.ID); err != nil {
		return fmt.Errorf("delete %s: %w", s.ID, err)
	}
	m.log("removed %s", s.ID)
	if s.RunnerName() != "" {
		if err := m.GitHub.DeleteOwned(ctx, m.Config.OwnerLabel(), map[string]bool{s.RunnerName(): true}, nil); err != nil {
			m.recovered = false
			return fmt.Errorf("remove GitHub registration for %s: %w", s.ID, err)
		}
	}
	return nil
}
func (m *Manager) Cleanup(ctx context.Context) error {
	items, err := m.Runtime.List(ctx)
	if err != nil {
		return fmt.Errorf("cleanup list: %w", err)
	}
	var failures []error
	keep := map[string]bool{}
	for _, s := range items {
		if !s.Owned(m.Config.Owner()) {
			continue
		}
		if err := m.remove(ctx, s); err != nil {
			failures = append(failures, err)
			// Do not deregister a runner whose container could still be running.
			if s.RunnerName() != "" {
				keep[s.RunnerName()] = true
			}
		}
	}
	if err := m.GitHub.DeleteOwned(ctx, m.Config.OwnerLabel(), nil, keep); err != nil {
		failures = append(failures, fmt.Errorf("cleanup GitHub registrations: %w", err))
	}
	return errors.Join(failures...)
}
func (m *Manager) Serve(ctx context.Context) error {
	if err := m.Reconcile(ctx); err != nil {
		m.log("reconcile: %v", err)
	}
	ticker := time.NewTicker(time.Duration(m.Config.Runtime.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log("shutdown: stopping and deleting managed runners")
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err := m.Cleanup(cleanup)
			cancel()
			return err
		case <-ticker.C:
			if err := m.Reconcile(ctx); err != nil {
				m.log("reconcile: %v", err)
			}
		}
	}
}
