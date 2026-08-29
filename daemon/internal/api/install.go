package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"ptero-native/internal/server"
	"ptero-native/internal/util"
)

// installJob tracks a running install script per server.
type installJob struct {
	cancel  context.CancelFunc
	buf     *server.Ring
	running bool
	mu      sync.Mutex
}

type installTracker struct {
	mu   sync.Mutex
	jobs map[string]*installJob
}

var installs = &installTracker{jobs: map[string]*installJob{}}

// TriggerInstall fetches the egg install script from the panel and runs it natively.
// When startOnCompletion is true the server is started after a successful install.
func (a *App) TriggerInstall(uuid string, reinstall bool, startOnCompletion bool) {
	s, ok := a.Registry.Get(uuid)
	if !ok {
		return
	}

	installs.mu.Lock()
	if j, running := installs.jobs[uuid]; running && j.running {
		installs.mu.Unlock()
		a.Log.Warn("install already running for %s", uuid)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &installJob{cancel: cancel, buf: server.NewRing(1000), running: true}
	installs.jobs[uuid] = job
	installs.mu.Unlock()

	go func() {
		defer func() {
			installs.mu.Lock()
			job.running = false
			installs.mu.Unlock()
		}()
		a.runInstall(ctx, s, job, reinstall, startOnCompletion)
	}()
}

// runInstall executes the egg install script natively and reports to the panel.
func (a *App) runInstall(ctx context.Context, s *server.Server, job *installJob, reinstall bool, startOnCompletion bool) {
	uuid := s.UUID()
	s.SetInstalled(0)
	st := s.Snapshot()
	if st.State != server.StateOffline {
		_ = s.Stop()
		for i := 0; i < 60; i++ {
			if s.State() == server.StateOffline || s.State() == server.StateCrashed {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	s.SetState(server.StateInstalling)
	a.emitInstallStatus(uuid, "running")

	// fetch install definition from panel
	def, err := a.Panel.GetInstallScript(uuid)
	if err != nil {
		a.Log.Error("install %s: fetch script: %v", uuid, err)
		a.failInstall(s, job, "fetch install script: "+err.Error())
		return
	}

	script := jsonStr(def, "script")

	if script == "" {
		// no install script: mark success immediately
		s.SetInstalled(1)
		s.SetState(server.StateOffline)
		a.emitInstallStatus(uuid, "done")
		a.emitInstallOutput(uuid, "[ptero-native] no install script; skipping")
		if err := a.Panel.ReportInstall(uuid, true, false); err != nil {
			a.Log.Warn("install success callback failed for %s: %v", uuid, err)
		}
		return
	}


	vol := a.Cfg.ServerVolume(uuid)
	_ = os.MkdirAll(vol, 0o755)

	// write script file
	scriptPath := filepath.Join(vol, ".ptero-install.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		a.failInstall(s, job, "write install script: "+err.Error())
		return
	}
	defer func() { _ = os.Remove(scriptPath) }()

	// environment: build env + install-time extras
	env := s.BuildEnv()
	env = append(env, "PWD="+vol, "HOME="+vol)

	// disk guard: refuse install if it would exceed quota by 5x limit safety
	quota := s.Quota()
	if q, ok := quota.(interface{ LimitBytes(*server.Server) int64 }); ok {
		_ = q
	}

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	cmd.Dir = vol
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.failInstall(s, job, "stdout pipe: "+err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.failInstall(s, job, "stderr pipe: "+err.Error())
		return
	}

	a.emitInstallOutput(uuid, "[ptero-native] starting native install")
	if err := cmd.Start(); err != nil {
		a.failInstall(s, job, "start install: "+err.Error())
		return
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// pump output
	var wg sync.WaitGroup
	wg.Add(2)
	go a.pumpInstall(&wg, stdout, uuid, job)
	go a.pumpInstall(&wg, stderr, uuid, job)

	// install timeout: 30 minutes
	timer := time.NewTimer(30 * time.Minute)
	defer timer.Stop()

	var waitErr error
	var timedOut bool
selectLoop:
	for {
		select {
		case waitErr = <-done:
			wg.Wait()
			break selectLoop
		case <-timer.C:
			timedOut = true
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			wg.Wait()
			<-done
			break selectLoop
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			wg.Wait()
			<-done
			a.failInstall(s, job, "install cancelled")
			return
		}
	}

	if timedOut {
		a.failInstall(s, job, "install timed out after 30m")
		return
	}

	if waitErr != nil {
		msg := "install script failed"
		if ee, ok := waitErr.(*exec.ExitError); ok {
			msg = fmt.Sprintf("install script exited with code %d", ee.ExitCode())
		}
		a.failInstall(s, job, msg)
		return
	}

	s.SetInstalled(1)
	s.SetState(server.StateOffline)
	a.emitInstallStatus(uuid, "done")
	a.emitInstallOutput(uuid, "[ptero-native] install completed successfully")
	s.PushConsole("[ptero-native] install completed successfully")
	if err := a.Panel.ReportInstall(uuid, true, false); err != nil {
		a.Log.Warn("install success callback failed for %s: %v", uuid, err)
	}
}

// failInstall marks the install failed (state + panel callback).
func (a *App) failInstall(s *server.Server, job *installJob, msg string) {
	uuid := s.UUID()
	s.SetInstalled(2)
	s.SetState(server.StateOffline)
	a.emitInstallStatus(uuid, "failed")
	a.emitInstallOutput(uuid, "[ptero-native] "+msg)
	s.PushConsole("[ptero-native] install failed: " + msg)
	a.Log.Error("install %s failed: %s", uuid, msg)
	if err := a.Panel.ReportInstall(uuid, false, false); err != nil {
		a.Log.Warn("install failed callback failed for %s: %v", uuid, err)
	}
}

// pumpInstall streams install output to the hub + buffer.
func (a *App) pumpInstall(wg *sync.WaitGroup, r io.Reader, uuid string, job *installJob) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 512*1024)
	for sc.Scan() {
		line := util.TruncateLine(sc.Text(), 4096)
		job.mu.Lock()
		job.buf.Push(line)
		job.mu.Unlock()
		a.emitInstallOutput(uuid, line)
	}
}

// emitInstallOutput sends install output through the hub (nil-safe).
func (a *App) emitInstallOutput(uuid, line string) {
	if a.Hub != nil {
		a.Hub.InstallOutput(uuid, line)
	}
}

// emitInstallStatus sends install status through the hub (nil-safe).
func (a *App) emitInstallStatus(uuid, status string) {
	if a.Hub != nil {
		a.Hub.InstallStatus(uuid, status)
	}
}

// jsonStr extracts a string field.
func jsonStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// jsonBool extracts a bool field.
func jsonBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// installLogReplay returns buffered install output for a server (nil-safe).
func installLogReplay(uuid string) []string {
	installs.mu.Lock()
	defer installs.mu.Unlock()
	job, ok := installs.jobs[uuid]
	if !ok || job == nil {
		return nil
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.buf.Replay(0, true)
}
