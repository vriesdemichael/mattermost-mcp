// Command stack runs this checkout's live-test Mattermost instances (ADR-007).
//
//	go run ./tools/stack up     [-stack latest|esr] [-release 11.9.4]
//	go run ./tools/stack down   [-stack ...] [-release ...]
//	go run ./tools/stack reset  [-stack ...] [-release ...]
//	go run ./tools/stack status [-stack ...] [-release ...]
//	go run ./tools/stack logs   [-stack ...] [-release ...]
//	go run ./tools/stack prune
//
// Each checkout has its own instances, so worktrees never share one; the
// package doc of internal/teststack says how they are named and where they
// listen. Written in Go rather than shell so it runs the same on Windows.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vriesdemichael/mattermost-mcp/internal/teststack"
)

// maxInstancesDefault bounds how many instances run on one machine. Each is a
// Mattermost and a PostgreSQL of a few hundred megabytes.
const maxInstancesDefault = 6

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: stack up|down|reset|status|logs|prune [-stack latest|esr] [-release X.Y.Z]"))
	}
	command := os.Args[1]
	flags := flag.NewFlagSet("stack "+command, flag.ExitOnError)
	stack := flags.String("stack", "latest", "the pinned stack: latest or esr")
	release := flags.String("release", "", "a Mattermost release to run instead, from the stack's compose file")
	_ = flags.Parse(os.Args[2:])

	checkout, err := currentCheckout()
	if err != nil {
		fail(err)
	}
	if command == "prune" {
		fail(prune(""))
		return
	}
	instance, err := teststack.Resolve(checkout, *stack, *release)
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	switch command {
	case "up":
		err = up(ctx, checkout, instance)
	case "down":
		err = compose(instance, "down")
		_ = os.Remove(filepath.Join(checkout.Root, instance.StateFile))
	case "reset":
		if err = compose(instance, "down", "--volumes", "--remove-orphans"); err == nil {
			err = up(ctx, checkout, instance)
		}
	case "status":
		err = status(checkout, instance)
	case "logs":
		err = compose(instance, "logs", "--tail", "200", "mattermost")
	default:
		err = fmt.Errorf("unknown command %q", command)
	}
	fail(err)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "stack: %v\n", err)
		os.Exit(1)
	}
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func currentCheckout() (teststack.Checkout, error) {
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return teststack.Checkout{}, fmt.Errorf("not inside a git checkout: %w", err)
	}
	gitDir, err := git("rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return teststack.Checkout{}, err
	}
	commonDir, err := git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return teststack.Checkout{}, err
	}
	return teststack.Checkout{Root: filepath.FromSlash(root), Linked: filepath.Clean(gitDir) != filepath.Clean(commonDir)}, nil
}

func composeArgs(instance teststack.Instance, args ...string) []string {
	base := []string{"compose", "-p", instance.Project, "-f", filepath.Join(instance.Worktree, "docker", instance.Stack, "compose.yml")}
	if instance.Release != "" {
		base = append(base, "-f", overridePath(instance))
	}
	return append(base, args...)
}

func overridePath(instance teststack.Instance) string {
	return filepath.Join(instance.Worktree, ".tmp", "stack-release-"+instance.Release+".override.yml")
}

func composeCommand(instance teststack.Instance, args ...string) *exec.Cmd {
	cmd := exec.Command("docker", composeArgs(instance, args...)...)
	cmd.Env = append(os.Environ(),
		"MM_HOST_PORT="+strconv.Itoa(instance.HostPort),
		"MM_STACK_WORKTREE="+filepath.ToSlash(instance.Worktree),
	)
	return cmd
}

func compose(instance teststack.Instance, args ...string) error {
	cmd := composeCommand(instance, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func up(ctx context.Context, checkout teststack.Checkout, instance teststack.Instance) error {
	started := time.Now()
	if err := prune(instance.Project); err != nil {
		return err
	}
	if err := refusePastTheLimit(instance); err != nil {
		return err
	}
	if instance.Release != "" {
		if err := os.MkdirAll(filepath.Dir(overridePath(instance)), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(overridePath(instance), []byte(teststack.ReleaseOverride(instance.Release)), 0o600); err != nil {
			return err
		}
	}
	// Not --wait: the image's healthcheck polls every thirty seconds, and
	// Bootstrap polls the ping each second, which is what readiness means here.
	if err := compose(instance, "up", "--detach", "--quiet-pull"); err != nil {
		return fmt.Errorf("the stack did not come up; `task stack:logs` shows why: %w", err)
	}
	port, err := publishedPort(instance)
	if err != nil {
		return err
	}
	url := "http://localhost:" + port
	if err := teststack.WriteState(filepath.Join(checkout.Root, instance.StateFile), url); err != nil {
		return err
	}
	if err := teststack.Bootstrap(ctx, url); err != nil {
		return err
	}
	fmt.Printf("%s is ready at %s, administrator %s, in %s.\n",
		instance.Project, url, teststack.AdminUsername, time.Since(started).Round(100*time.Millisecond))
	return nil
}

// publishedPort is the host port Docker gave the instance, which changes each
// time a container with an assigned port starts.
func publishedPort(instance teststack.Instance) (string, error) {
	var out bytes.Buffer
	cmd := composeCommand(instance, "port", "mattermost", "8065")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not read the port Mattermost was published on: %w", err)
	}
	address := strings.TrimSpace(strings.Split(out.String(), "\n")[0])
	port := address[strings.LastIndex(address, ":")+1:]
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("docker compose port answered %q", address)
	}
	return port, nil
}

type labelled struct{ project, worktree, state string }

// instances lists every container any checkout started, one row per project.
func instances() ([]labelled, error) {
	out, err := exec.Command("docker", "ps", "--all", "--filter", "label="+teststack.WorktreeLabel,
		"--format", "{{.Label \"com.docker.compose.project\"}}\t{{.Label \""+teststack.WorktreeLabel+"\"}}\t{{.State}}").Output()
	if err != nil {
		return nil, fmt.Errorf("could not list containers: %w", err)
	}
	seen := map[string]labelled{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" {
			continue
		}
		// A project counts as running while any of its containers runs.
		if existing, ok := seen[fields[0]]; ok && existing.state == "running" {
			continue
		}
		seen[fields[0]] = labelled{project: fields[0], worktree: fields[1], state: fields[2]}
	}
	var rows []labelled
	for _, row := range seen {
		rows = append(rows, row)
	}
	return rows, nil
}

// prune takes down other checkouts' instances that have stopped: with their
// data when the worktree is gone, keeping it otherwise. A stopped instance still
// holds a compose network, and each network holds a subnet from Docker's pools.
// A running instance is never touched: its worktree may only look gone from
// this environment, such as a path WSL recorded seen from Windows.
func prune(own string) error {
	rows, err := instances()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.project == own || (row.state != "exited" && row.state != "dead") {
			continue
		}
		args := []string{"compose", "-p", row.project, "down"}
		if _, err := os.Stat(filepath.FromSlash(row.worktree)); errors.Is(err, os.ErrNotExist) {
			fmt.Printf("Removing %s: it has stopped, and its worktree %s no longer exists.\n", row.project, row.worktree)
			args = append(args, "--volumes")
		} else {
			fmt.Printf("Taking down %s: it has stopped, and its network is only in the way.\n", row.project)
		}
		if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("could not take down %s: %w: %s", row.project, err, out)
		}
	}
	return pruneVolumes()
}

// pruneVolumes removes the data a deleted worktree's instances left behind
// after they were taken down, which no container points at any more. A volume
// a container still uses is left alone: docker refuses to remove it.
func pruneVolumes() error {
	out, err := exec.Command("docker", "volume", "ls", "--filter", "label="+teststack.WorktreeLabel,
		"--format", "{{.Name}}\t{{.Label \""+teststack.WorktreeLabel+"\"}}").Output()
	if err != nil {
		return fmt.Errorf("could not list volumes: %w", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		name, worktree, ok := strings.Cut(line, "\t")
		if !ok || worktree == "" {
			continue
		}
		if _, err := os.Stat(filepath.FromSlash(worktree)); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if exec.Command("docker", "volume", "rm", name).Run() == nil {
			fmt.Printf("Removed volume %s: its worktree %s no longer exists.\n", name, worktree)
		}
	}
	return nil
}

func refusePastTheLimit(instance teststack.Instance) error {
	limit := maxInstancesDefault
	if raw := os.Getenv("MM_STACK_MAX"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("MM_STACK_MAX must be a number, not %q", raw)
		}
		limit = parsed
	}
	rows, err := instances()
	if err != nil {
		return err
	}
	running := 0
	for _, row := range rows {
		if row.state == "running" && row.project != instance.Project {
			running++
		}
	}
	if running >= limit {
		return fmt.Errorf("%d live-test instances already run on this machine, and it holds at most %d (MM_STACK_MAX); stop one with `task stack:down` in its worktree", running, limit)
	}
	return nil
}

func status(checkout teststack.Checkout, instance teststack.Instance) error {
	if err := compose(instance, "ps"); err != nil {
		return err
	}
	if url, err := teststack.ReadState(filepath.Join(checkout.Root, instance.StateFile)); err == nil {
		fmt.Printf("\nThis checkout's %s instance: %s\n", instance.Project, url)
	}
	rows, err := instances()
	if err != nil {
		return err
	}
	fmt.Println("\nLive-test instances on this machine:")
	for _, row := range rows {
		fmt.Printf("  %-40s %-8s %s\n", row.project, row.state, row.worktree)
	}
	return nil
}
