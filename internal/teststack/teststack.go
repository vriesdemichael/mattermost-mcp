// Package teststack describes the local Mattermost instances the live suite
// runs against, one set per checkout (ADR-007), and bootstraps one.
//
// Git worktrees let several agents work on this repository at once. Sharing one
// instance between them would let a restart in one end another's live run, so
// each checkout has its own: its own compose project, containers, database and
// data volume. The main checkout keeps the fixed ports the docs name, 8065 for
// the newest release and 8066 for the Extended Support Release. A linked
// worktree gets projects named after it and ports Docker assigns, which cannot
// collide. A release other than the two pinned ones, such as one between them,
// runs as a project of its own beside them, also on an assigned port.
//
// Docker assigns a new port each time a stopped container starts, so `up`
// writes the instance's address to a state file under .tmp/ every time, and the
// live suite reads it from there.
package teststack

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // names a project after a path; not a security use
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Stacks are the two pinned releases, each with its compose file under docker/.
var Stacks = []string{"latest", "esr"}

// MainPorts are the host ports the main checkout's instances listen on.
var MainPorts = map[string]int{"latest": 8065, "esr": 8066}

// WorktreeLabel marks every container with the checkout that started it.
const WorktreeLabel = "dev.mm-mcp.worktree"

// The stack's system administrator. Well known on purpose: the instance is
// disposable and listens on the loopback address only.
const (
	AdminUsername = "sysadmin"
	AdminPassword = "Sysadmin-password-1!" //nolint:gosec // a disposable test instance
	AdminEmail    = "sysadmin@example.com"
)

// ReadyTimeout bounds how long Bootstrap waits for the instance to answer.
const ReadyTimeout = 5 * time.Minute

// StateURLKey is the state file's line that holds the instance's address.
const StateURLKey = "MATTERMOST_URL"

var releasePattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Instance is one Mattermost a checkout can run: a pinned stack, or a release.
type Instance struct {
	// Stack is the compose file it starts from: latest or esr.
	Stack string
	// Release, when set, replaces the stack's image tag.
	Release string
	// Project is the compose project name.
	Project string
	// HostPort is the port to publish, 0 for one Docker assigns.
	HostPort int
	// StateFile is where `up` writes its address, relative to the checkout.
	StateFile string
	// Worktree is the checkout's absolute path, recorded on its containers.
	Worktree string
}

// Checkout is what decides an instance's name and ports: where the checkout is,
// and whether it is the main one or a linked worktree.
type Checkout struct {
	Root   string
	Linked bool
}

// Resolve names the instance a checkout runs for a stack, or for a release
// started from that stack's compose file.
func Resolve(checkout Checkout, stack, release string) (Instance, error) {
	if !slices.Contains(Stacks, stack) {
		return Instance{}, fmt.Errorf("no stack named %q; the stacks are %s", stack, strings.Join(Stacks, " and "))
	}
	if release != "" && !releasePattern.MatchString(release) {
		return Instance{}, fmt.Errorf("%q is not a Mattermost release; give one such as 11.9.4", release)
	}
	instance := Instance{Stack: stack, Release: release, Worktree: checkout.Root, Project: "mm-mcp"}
	if checkout.Linked {
		instance.Project += "-" + worktreeSlug(checkout.Root)
	}
	if release != "" {
		instance.Project += "-release-" + strings.ReplaceAll(release, ".", "-")
		instance.StateFile = filepath.Join(".tmp", "stack-release-"+release+".env")
		return instance, nil
	}
	instance.Project += "-" + stack
	instance.StateFile = filepath.Join(".tmp", "stack-"+stack+".env")
	if !checkout.Linked {
		instance.HostPort = MainPorts[stack]
	}
	return instance, nil
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// worktreeSlug names a linked worktree's projects: its directory's name, cut
// short, and a hash of its full path, so two worktrees of the same name differ.
func worktreeSlug(root string) string {
	name := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(filepath.Base(root)), "-"), "-")
	if len(name) > 24 {
		name = strings.TrimRight(name[:24], "-")
	}
	sum := sha1.Sum([]byte(filepath.ToSlash(root))) //nolint:gosec // see the import
	return name + "-" + hex.EncodeToString(sum[:])[:6]
}

// ReleaseOverride is the compose override that starts a stack's file with
// another release's image.
func ReleaseOverride(release string) string {
	return "services:\n  mattermost:\n    image: mattermost/mattermost-team-edition:" + release + "\n"
}

// WriteState records where an instance listens.
func WriteState(path, url string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	content := "# Written by `task stack:up`: this checkout's own Mattermost.\n" + StateURLKey + "=" + url + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

// ReadState is the address a state file records.
func ReadState(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // a path this package builds
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), StateURLKey+"="); ok {
			return value, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s records no %s", path, StateURLKey)
}

// Bootstrap waits for the instance at address to answer and creates its
// administrator, unless it exists. On an instance already bootstrapped it only
// logs in, which Mattermost records as an update to the administrator. Each live test seeds and removes its own users, bots and
// channels (ADR-008); this creates only what no test owns.
func Bootstrap(ctx context.Context, address string) error {
	client := model.NewAPIv4Client(address)
	client.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	if err := waitUntilReady(ctx, client, ReadyTimeout); err != nil {
		return err
	}
	if _, _, err := client.Login(ctx, AdminUsername, AdminPassword); err == nil {
		return nil
	}
	// The first user an instance creates becomes its system administrator.
	if _, _, err := client.CreateUser(ctx, &model.User{Username: AdminUsername, Password: AdminPassword, Email: AdminEmail}); err != nil {
		return fmt.Errorf("could not log in as %s or create it (%w); `task stack:reset` starts over", AdminUsername, err)
	}
	admin, _, err := client.Login(ctx, AdminUsername, AdminPassword)
	if err != nil {
		return fmt.Errorf("created %s but could not log in as it: %w", AdminUsername, err)
	}
	if !slices.Contains(strings.Fields(admin.Roles), model.SystemAdminRoleId) {
		return fmt.Errorf("%s was created but is not a system administrator (%q): the instance already had users; `task stack:reset` starts over", AdminUsername, admin.Roles)
	}
	return nil
}

func waitUntilReady(ctx context.Context, client *model.Client4, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := errors.New("no answer yet")
	for time.Now().Before(deadline) {
		status, _, err := client.GetPing(ctx)
		if err == nil && status == model.StatusOk {
			return nil
		}
		if err != nil {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("no answer to the ping from Mattermost at %s within %s: %w", client.URL, timeout, last)
}
