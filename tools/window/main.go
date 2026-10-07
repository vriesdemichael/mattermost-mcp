// Command window lists the Mattermost releases in the supported window that the
// live suite does not run on every pull request (ADR-025).
//
//	go run ./tools/window            # a JSON array, for a workflow's matrix
//
// The window's ends are the releases docker/esr and docker/latest run. Between
// them it takes the newest published patch of every minor release, read from
// Docker Hub's tags for the Team Edition image, and leaves out the two ends,
// which run on every pull request. The weekly workflow runs the live suite
// against each.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const tagsURL = "https://hub.docker.com/v2/repositories/mattermost/mattermost-team-edition/tags?page_size=100&ordering=last_updated"

var (
	imageTag = regexp.MustCompile(`(?m)image:\s*mattermost/mattermost-team-edition:(\d+\.\d+\.\d+)\s*$`)
	release  = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
)

// Release is a published Mattermost release.
type Release struct{ Major, Minor, Patch int }

func (r Release) String() string { return fmt.Sprintf("%d.%d.%d", r.Major, r.Minor, r.Patch) }

func (r Release) less(other Release) bool {
	if r.Major != other.Major {
		return r.Major < other.Major
	}
	if r.Minor != other.Minor {
		return r.Minor < other.Minor
	}
	return r.Patch < other.Patch
}

// Parse reads a release tag such as 11.9.4; anything else, such as a release
// candidate or a branch tag, is not a release.
func Parse(tag string) (Release, bool) {
	m := release.FindStringSubmatch(tag)
	if m == nil {
		return Release{}, false
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	return Release{n(1), n(2), n(3)}, true
}

// Between is the newest patch of each minor release from oldest to newest,
// inclusive of their minors, without the two ends themselves.
func Between(tags []string, oldest, newest Release) []Release {
	best := map[[2]int]Release{}
	for _, tag := range tags {
		r, ok := Parse(tag)
		if !ok || r.less(Release{oldest.Major, oldest.Minor, 0}) || newest.less(r) {
			continue
		}
		key := [2]int{r.Major, r.Minor}
		if current, seen := best[key]; !seen || current.less(r) {
			best[key] = r
		}
	}
	var picked []Release
	for _, r := range best {
		if r != oldest && r != newest {
			picked = append(picked, r)
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].less(picked[j]) })
	return picked
}

func main() {
	oldest, err := stackRelease("esr")
	fail(err)
	newest, err := stackRelease("latest")
	fail(err)
	tags, err := publishedTags()
	fail(err)
	names := []string{}
	for _, r := range Between(tags, oldest, newest) {
		names = append(names, r.String())
	}
	encoded, err := json.Marshal(names)
	fail(err)
	fmt.Println(string(encoded))
}

func stackRelease(stack string) (Release, error) {
	compose, err := os.ReadFile(filepath.Join("docker", stack, "compose.yml")) //nolint:gosec // a fixed path
	if err != nil {
		return Release{}, err
	}
	m := imageTag.FindSubmatch(compose)
	if m == nil {
		return Release{}, fmt.Errorf("docker/%s/compose.yml names no mattermost-team-edition release", stack)
	}
	r, _ := Parse(string(m[1]))
	return r, nil
}

func publishedTags() ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var tags []string
	url := tagsURL
	for page := 0; url != "" && page < 20; page++ {
		response, err := client.Get(url) //nolint:noctx // a command-line tool with a client timeout
		if err != nil {
			return nil, err
		}
		var body struct {
			Next    string `json:"next"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		err = json.NewDecoder(response.Body).Decode(&body)
		_ = response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("Docker Hub answered something other than a tag list: %w", err) //nolint:staticcheck // a proper noun
		}
		for _, result := range body.Results {
			tags = append(tags, result.Name)
		}
		url = body.Next
	}
	return tags, nil
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "window: %v\n", err)
		os.Exit(1)
	}
}
