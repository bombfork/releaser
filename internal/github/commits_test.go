package github_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	gh "github.com/google/go-github/v86/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"

	releasergh "github.com/bombfork/releaser/internal/github"
)

// commitBody mirrors the wire shape go-github sends to CreateCommit
// (tree and parents are SHAs, not nested objects). Used for decoding
// captured request bodies in tests.
type commitBody struct {
	Author    *gh.CommitAuthor `json:"author,omitempty"`
	Committer *gh.CommitAuthor `json:"committer,omitempty"`
	Message   string           `json:"message,omitempty"`
	Tree      string           `json:"tree,omitempty"`
	Parents   []string         `json:"parents,omitempty"`
}

// gitDataCapture stores decoded request bodies for assertions.
type gitDataCapture struct {
	blobs        []gh.Blob
	treeEntries  []gh.TreeEntry
	treeBaseSHAs []string
	commit       commitBody
	updateRef    gh.UpdateRef
	createRef    gh.CreateRef
	updateCalls  atomic.Int32
	createCalls  atomic.Int32
	getCommitSHA string
}

// gitDataMock wires the six git-data endpoints CreateSignedCommit hits
// and returns the capture struct so tests can inspect what was sent.
// updateRefFails, if true, makes UpdateRef return 404 so CreateRef
// becomes the fallback path.
func gitDataMock(t *testing.T, updateRefFails bool) (*http.Client, *gitDataCapture) {
	t.Helper()
	cap := &gitDataCapture{}

	blobIdx := 0
	handlers := []mock.MockBackendOption{
		mock.WithRequestMatchHandler(
			mock.GetReposGitCommitsByOwnerByRepoByCommitSha,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				parts := strings.Split(r.URL.Path, "/")
				cap.getCommitSHA = parts[len(parts)-1]
				_ = json.NewEncoder(w).Encode(gh.Commit{
					SHA:  gh.Ptr(cap.getCommitSHA),
					Tree: &gh.Tree{SHA: gh.Ptr("base-tree-sha")},
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitBlobsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body gh.Blob
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode blob body: %v", err)
				}
				cap.blobs = append(cap.blobs, body)
				blobIdx++
				_ = json.NewEncoder(w).Encode(gh.Blob{SHA: gh.Ptr(blobSHA(blobIdx))})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitTreesByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					BaseTree string         `json:"base_tree"`
					Tree     []gh.TreeEntry `json:"tree"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode tree body: %v", err)
				}
				cap.treeBaseSHAs = append(cap.treeBaseSHAs, body.BaseTree)
				cap.treeEntries = append(cap.treeEntries, body.Tree...)
				_ = json.NewEncoder(w).Encode(gh.Tree{SHA: gh.Ptr("new-tree-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitCommitsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &cap.commit)
				_ = json.NewEncoder(w).Encode(gh.Commit{SHA: gh.Ptr("new-commit-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PatchReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cap.updateCalls.Add(1)
				if updateRefFails {
					mock.WriteError(w, http.StatusNotFound, "ref not found")
					return
				}
				_ = json.NewDecoder(r.Body).Decode(&cap.updateRef)
				_ = json.NewEncoder(w).Encode(gh.Reference{
					Ref:    gh.Ptr(r.URL.Path),
					Object: &gh.GitObject{SHA: gh.Ptr("new-commit-sha")},
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitRefsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cap.createCalls.Add(1)
				_ = json.NewDecoder(r.Body).Decode(&cap.createRef)
				_ = json.NewEncoder(w).Encode(gh.Reference{
					Ref:    gh.Ptr(cap.createRef.Ref),
					Object: &gh.GitObject{SHA: gh.Ptr("new-commit-sha")},
				})
			}),
		),
	}
	return mock.NewMockedHTTPClient(handlers...), cap
}

func blobSHA(i int) string { return "blob-sha-" + string(rune('0'+i)) }

func TestCreateSignedCommit_HappyPath(t *testing.T) {
	httpClient, cap := gitDataMock(t, false)
	c := releasergh.NewClient(httpClient)

	files := []releasergh.FileChange{
		{Path: "Makefile", Content: []byte("VERSION := 0.2.0\nall:\n"), Mode: "100644"},
		{Path: "scripts/release.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Mode: "100755"},
	}
	newSHA, err := c.CreateSignedCommit(context.Background(), "owner", "repo",
		"releaser/pending-release", "parent-sha", files,
		"chore(release): prepare v0.2.0", "github-actions[bot]", "bot@example.com")
	if err != nil {
		t.Fatalf("CreateSignedCommit: %v", err)
	}
	if newSHA != "new-commit-sha" {
		t.Errorf("newSHA = %q, want new-commit-sha", newSHA)
	}

	// Parent commit was fetched for its tree.
	if cap.getCommitSHA != "parent-sha" {
		t.Errorf("GetCommit hit with %q, want parent-sha", cap.getCommitSHA)
	}

	// One blob per file, base64-decoded matches input.
	if len(cap.blobs) != 2 {
		t.Fatalf("captured %d blob calls, want 2", len(cap.blobs))
	}
	for i, want := range []string{"VERSION := 0.2.0\nall:\n", "#!/bin/sh\nexit 0\n"} {
		decoded, err := base64.StdEncoding.DecodeString(*cap.blobs[i].Content)
		if err != nil {
			t.Fatalf("blob %d not base64: %v", i, err)
		}
		if string(decoded) != want {
			t.Errorf("blob %d = %q, want %q", i, decoded, want)
		}
		if cap.blobs[i].GetEncoding() != "base64" {
			t.Errorf("blob %d encoding = %q, want base64", i, cap.blobs[i].GetEncoding())
		}
	}

	// Tree built off base-tree-sha with two entries carrying the right modes.
	if len(cap.treeBaseSHAs) != 1 || cap.treeBaseSHAs[0] != "base-tree-sha" {
		t.Errorf("tree base SHAs = %v, want [base-tree-sha]", cap.treeBaseSHAs)
	}
	if len(cap.treeEntries) != 2 {
		t.Fatalf("captured %d tree entries, want 2", len(cap.treeEntries))
	}
	if got := cap.treeEntries[0].GetPath(); got != "Makefile" {
		t.Errorf("entry[0].Path = %q, want Makefile", got)
	}
	if got := cap.treeEntries[0].GetMode(); got != "100644" {
		t.Errorf("entry[0].Mode = %q, want 100644", got)
	}
	if got := cap.treeEntries[1].GetMode(); got != "100755" {
		t.Errorf("entry[1].Mode = %q, want 100755", got)
	}

	// Commit body carries identity, parent, tree.
	if got := cap.commit.Message; got != "chore(release): prepare v0.2.0" {
		t.Errorf("commit message = %q", got)
	}
	if got := cap.commit.Author.GetName(); got != "github-actions[bot]" {
		t.Errorf("author name = %q", got)
	}
	if got := cap.commit.Committer.GetEmail(); got != "bot@example.com" {
		t.Errorf("committer email = %q", got)
	}
	if len(cap.commit.Parents) != 1 || cap.commit.Parents[0] != "parent-sha" {
		t.Errorf("commit parents = %v, want [parent-sha]", cap.commit.Parents)
	}
	if cap.commit.Tree != "new-tree-sha" {
		t.Errorf("commit tree SHA = %q, want new-tree-sha", cap.commit.Tree)
	}

	// UpdateRef called once with the new SHA + force; CreateRef not called.
	if got := cap.updateCalls.Load(); got != 1 {
		t.Errorf("UpdateRef calls = %d, want 1", got)
	}
	if got := cap.createCalls.Load(); got != 0 {
		t.Errorf("CreateRef calls = %d, want 0 on happy path", got)
	}
	if cap.updateRef.SHA != "new-commit-sha" {
		t.Errorf("UpdateRef.SHA = %q, want new-commit-sha", cap.updateRef.SHA)
	}
	if cap.updateRef.Force == nil || !*cap.updateRef.Force {
		t.Errorf("UpdateRef.Force = %v, want true", cap.updateRef.Force)
	}
}

func TestCreateSignedCommit_CreatesRefWhenBranchAbsent(t *testing.T) {
	httpClient, cap := gitDataMock(t, true) // UpdateRef returns 404
	c := releasergh.NewClient(httpClient)

	if _, err := c.CreateSignedCommit(context.Background(), "owner", "repo",
		"releaser/pending-release", "parent-sha",
		[]releasergh.FileChange{{Path: "Makefile", Content: []byte("VERSION := 0.2.0\n"), Mode: "100644"}},
		"msg", "bot", "bot@example.com"); err != nil {
		t.Fatalf("CreateSignedCommit: %v", err)
	}

	if got := cap.updateCalls.Load(); got != 1 {
		t.Errorf("UpdateRef calls = %d, want 1", got)
	}
	if got := cap.createCalls.Load(); got != 1 {
		t.Errorf("CreateRef calls = %d, want 1 after UpdateRef 404", got)
	}
	if cap.createRef.Ref != "refs/heads/releaser/pending-release" {
		t.Errorf("CreateRef.Ref = %q, want refs/heads/releaser/pending-release", cap.createRef.Ref)
	}
}

func TestCreateSignedCommit_EmptyFilesReusesParentTree(t *testing.T) {
	httpClient, cap := gitDataMock(t, false)
	c := releasergh.NewClient(httpClient)

	if _, err := c.CreateSignedCommit(context.Background(), "owner", "repo",
		"releaser/pending-release", "parent-sha", nil,
		"chore(release): prepare v0.2.0 (library mode)", "bot", "bot@example.com"); err != nil {
		t.Fatalf("CreateSignedCommit: %v", err)
	}

	if len(cap.blobs) != 0 {
		t.Errorf("captured %d blob calls, want 0 in library mode", len(cap.blobs))
	}
	if len(cap.treeBaseSHAs) != 0 {
		t.Errorf("captured %d CreateTree calls, want 0 in library mode", len(cap.treeBaseSHAs))
	}
	// Commit body still issued, with tree SHA equal to the parent's tree.
	if got := cap.commit.Tree; got != "base-tree-sha" {
		t.Errorf("commit tree SHA = %q, want base-tree-sha (parent's)", got)
	}
}

func TestCreateSignedCommit_RejectsUnsupportedMode(t *testing.T) {
	httpClient, _ := gitDataMock(t, false)
	c := releasergh.NewClient(httpClient)

	_, err := c.CreateSignedCommit(context.Background(), "owner", "repo",
		"branch", "parent-sha",
		[]releasergh.FileChange{{Path: "x", Content: []byte("y"), Mode: "120000"}},
		"msg", "bot", "bot@example.com")
	if err == nil || !strings.Contains(err.Error(), "unsupported mode") {
		t.Errorf("expected unsupported mode error, got %v", err)
	}
}
