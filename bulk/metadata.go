package bulk

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/shorthand/v2"
	"github.com/logrusorgru/aurora"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/afero"
	"github.com/spf13/viper"
)

const (
	metaDir  = ".rshbulk"
	metaFile = ".rshbulk" + string(os.PathSeparator) + "meta"
)

// commonPrefix finds the longest common directory prefix of a given set
// of URLs. The set of all strings after the prefix is guaranteed to be
// unique.
func commonPrefix(urls []listEntry) string {
	if len(urls) == 0 {
		return ""
	}

	prefix := strings.Split(urls[0].URL, "/")

	for _, entry := range urls[1:] {
		parts := strings.Split(entry.URL, "/")
		for i, part := range parts {
			if len(prefix) == i || prefix[i] != part {
				prefix = prefix[:i]
				break
			}
		}
	}

	return strings.Join(prefix, "/") + "/"
}

// getFirstKey returns the first found string key value for the given keys
// which are searched in order if item is a map. Returns an empty string if
// none are found.
func getFirstKey(item any, keys ...string) string {
	if m, ok := item.(map[string]any); ok {
		for _, k := range keys {
			if m[k] != nil {
				return fmt.Sprintf("%v", m[k])
			}
		}
	}
	if m, ok := item.(map[any]any); ok {
		for _, k := range keys {
			if m[k] != nil {
				return fmt.Sprintf("%v", m[k])
			}
		}
	}
	return ""
}

// listEntry represents a response from a list resources call.
type listEntry struct {
	URL     string `json:"url"`
	Version string `json:"version"`
}

type fileStatus uint8

const (
	// Terminal color codes
	statusAdded    = 150
	statusModified = 172
	statusRemoved  = 204
)

type changedFile struct {
	Status fileStatus
	File   *File
}

func (c changedFile) String() string {
	au := aurora.NewAurora(viper.GetBool("color"))
	label := map[fileStatus]string{
		statusAdded:    "added",
		statusModified: "modified",
		statusRemoved:  "removed",
	}[c.Status]
	return fmt.Sprintf("\t%8s:  %s", au.Index(uint8(c.Status), label), c.File.Path)
}

// Meta represents metadata about the remote and local status of the checkout.
type Meta struct {
	URL         string           `json:"url"`
	Filter      string           `json:"filter,omitempty"`
	Base        string           `json:"base,omitempty"`
	Schema      string           `json:"schema,omitempty"`
	URLTemplate string           `json:"url_template,omitempty"`
	Files       map[string]*File `json:"files,omitempty"`
}

// Save the metadata file to disk.
func (m *Meta) Save() error {
	b, err := cli.MarshalShort("json", true, m)
	if err != nil {
		return err
	}
	afs.MkdirAll(metaDir, 0700)
	return afero.WriteFile(afs, metaFile, b, 0600)
}

// Init initializes the metadata file, saves it to disk, and then performs
// the initial pull to fetch each file.
func (m *Meta) Init(url, template string) error {
	m.URL = cli.FixAddress(url)
	m.Filter = viper.GetString("rsh-filter")
	m.URLTemplate = template
	m.Files = map[string]*File{}

	if err := m.Save(); err != nil {
		return err
	}

	return m.Pull()
}

// PullIndex updates the index of remote files and their versions. It does not
// save the metadata file.
func (m *Meta) PullIndex() error {
	bar := progressbar.NewOptions(-1,
		progressbar.OptionSetWriter(cli.Stdout),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetDescription("Refreshing index..."),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionSetRenderBlankState(false),
	)

	done := make(chan bool)

	go func() {
		// Don't draw the spinner until the request has taken a short while already
		// to prevent a flash of text that immediately disappears.
		time.Sleep(250 * time.Millisecond)
		for {
			select {
			case <-done:
				bar.Clear()
				return
			default:
				bar.Add(1)
				time.Sleep(250 * time.Millisecond)
			}
		}
	}()

	defer func() {
		done <- true
	}()

	req, _ := http.NewRequest(http.MethodGet, m.URL, nil)
	parsed, err := cli.GetParsedResponse(req)
	if err != nil {
		panic(err)
	}
	var data any
	if m.Filter == "" {
		data = parsed.Body
	} else {
		opts := shorthand.GetOptions{}
		if viper.GetBool("rsh-verbose") {
			opts.DebugLogger = cli.LogDebug
		}

		result, _, err := shorthand.GetPath(m.Filter, parsed.Map(), opts)
		if err != nil {
			return err
		}

		data = result
	}

	if _, ok := data.([]any); !ok {
		panic("not a list")
	}

	var entries []listEntry

	for _, entry := range data.([]any) {
		// Try to get a {url, version} tuple from various possible common key names.
		url := getFirstKey(entry, "url", "uri", "self", "link")
		if url == "" && m.URLTemplate != "" {
			// We have a way to build the URL from other fields in the response.
			re := regexp.MustCompile(`\{[^}]+\}`)
			url = re.ReplaceAllStringFunc(m.URLTemplate, func(match string) string {
				match = strings.Trim(match, "{}")
				if m, ok := entry.(map[string]any); ok {
					return fmt.Sprintf("%v", m[match])
				}
				if m, ok := entry.(map[any]any); ok {
					return fmt.Sprintf("%v", m[match])
				}
				return ""
			})
		}

		version := getFirstKey(entry, "version", "etag", "last_modified", "lastModified", "modified")

		if (url == "") || (version == "") {
			panic("list response must contain URL and version for each resource")
		}
		entries = append(entries, listEntry{url, version})
	}

	baseURL, _ := url.Parse(m.URL)
	prefix, _ := url.Parse(commonPrefix(entries))
	m.Base = baseURL.ResolveReference(prefix).String()

	for _, f := range m.Files {
		// Clear all the remote versions, we will set them for files that exist
		// in the next step.
		f.VersionRemote = ""
	}

	for _, entry := range entries {
		u, _ := url.Parse(entry.URL)
		resolved := baseURL.ResolveReference(u).String()
		path := resolved[len(m.Base):] + ".json"
		f := m.Files[path]
		if f == nil {
			// Remote file was added.
			f = &File{
				Path: path,
				URL:  resolved,
			}
			m.Files[path] = f
		}
		f.VersionRemote = entry.Version
	}

	return nil
}

// Pull files from the remote. In the case of local changes this will update
// the index but *not* overwrite the local file containing the edits. When
// the pull completes, the metadata file is saved.
func (m *Meta) Pull() error {
	if err := m.PullIndex(); err != nil {
		return err
	}

	updates := []*File{}
	for _, f := range m.Files {
		if f.VersionLocal == f.VersionRemote {
			// No need to redownload this.
			continue
		}

		updates = append(updates, f)
	}

	if len(updates) == 0 {
		fmt.Fprintln(cli.Stdout, "Already up to date.")
		return nil
	}

	bar := progressbar.NewOptions(len(updates),
		progressbar.OptionSetWriter(cli.Stdout),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetDescription("Pulling resources..."),
	)

	for _, f := range updates {
		if f.VersionRemote == "" {
			// This was removed on the remote!
			delete(m.Files, f.Path)
			afs.Remove(f.Path)
			bar.Add(1)
			continue
		}

		b, err := f.Fetch()
		if err != nil {
			return err
		}

		// Don't overwrite local edits!
		if f.IsChangedLocal(true) {
			bar.Clear()
			fmt.Fprintln(cli.Stdout, "Skipping due to local edits:", f.Path)
			bar.Add(1)
			continue
		}

		if err := f.Write(b); err != nil {
			return err
		}

		bar.Add(1)
	}

	fmt.Fprintln(cli.Stdout)

	return m.Save()
}

// GetChanged calculates all the changed local and remote files using the
// following rules after refreshing the index:
// Remote:
// - Added: No local version or file
// - Changed: Local version != remote version
// - Removed: No remote version
// Local:
// - Added: Local file with no metadata entry
// - Changed: Local file hash != remote file hash
// - Removed: Metadata entry without local file
func (m *Meta) GetChanged(files []string) ([]changedFile, []changedFile, error) {
	if err := m.PullIndex(); err != nil {
		return nil, nil, err
	}

	filesMap := map[string]bool{}
	for _, path := range files {
		filesMap[path] = true
	}

	local := []changedFile{}
	remote := []changedFile{}

	for _, path := range files {
		if strings.HasPrefix(path, ".") {
			// Skip hidden dotfiles.
			continue
		}
		if f, ok := m.Files[path]; ok {
			if f.IsChangedLocal(true) {
				local = append(local, changedFile{statusModified, f})
			}
			if f.VersionRemote == "" {
				remote = append(remote, changedFile{statusRemoved, f})
			} else if f.VersionLocal != f.VersionRemote {
				remote = append(remote, changedFile{statusModified, f})
			}
		} else {
			local = append(local, changedFile{
				statusAdded, &File{
					Path: path,
					URL:  m.Base + path,
				},
			})
		}
	}

	for _, f := range m.Files {
		if f.VersionLocal == "" {
			remote = append(remote, changedFile{statusAdded, f})
		} else {
			if !filesMap[f.Path] {
				local = append(local, changedFile{statusRemoved, f})
			}
		}
	}

	// Because deleted files would be appended, we need to sort!
	sort.Slice(local, func(i, j int) bool {
		return local[i].File.Path < local[j].File.Path
	})

	return local, remote, nil
}

// Push uploads changed files to the server, using conditional updates when
// possible.
func (m *Meta) Push() error {
	local, _, err := m.GetChanged(collectFiles(m, []string{}, "", false))
	if err != nil {
		return err
	}

	bar := progressbar.NewOptions(len(local),
		progressbar.OptionSetWriter(cli.Stdout),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetDescription("Pushing resources..."),
	)

	for _, changed := range local {
		f := changed.File
		if changed.Status == statusModified || changed.Status == statusAdded {
			body, _ := afero.ReadFile(afs, f.Path)
			req, _ := http.NewRequest(http.MethodPut, f.URL, bytes.NewReader(body))

			if f.ETag != "" {
				req.Header.Set("If-Match", f.ETag)
			} else if f.LastModified != "" {
				req.Header.Set("If-Unmodified-Since", f.LastModified)
			}

			resp, err := cli.GetParsedResponse(req)
			if err != nil {
				return err
			}
			if resp.Status >= 400 {
				fmt.Fprintf(cli.Stdout, "Error uploading %s to %s\n", f.Path, f.URL)
				cli.Formatter.Format(resp)
				return err
			}

			if changed.Status == statusAdded {
				// Add the file to the metadata
				m.Files[changed.File.Path] = changed.File
			}

			// Fetch and write the updated metadata/file to disk.
			b, err := f.Fetch()
			if err != nil {
				return err
			}
			if err := f.Write(b); err != nil {
				return err
			}
		} else {
			req, _ := http.NewRequest(http.MethodDelete, f.URL, nil)

			if f.ETag != "" {
				req.Header.Set("If-Match", f.ETag)
			} else if f.LastModified != "" {
				req.Header.Set("If-Unmodified-Since", f.LastModified)
			}

			resp, err := cli.GetParsedResponse(req)
			if err != nil {
				return err
			}
			if resp.Status >= 400 {
				fmt.Fprintf(cli.Stdout, "Error uploading %s to %s\n", f.Path, f.URL)
				cli.Formatter.Format(resp)
				return err
			}
			delete(m.Files, f.Path)
		}
		bar.Add(1)
	}

	fmt.Fprintln(cli.Stdout)

	if err := m.PullIndex(); err != nil {
		return err
	}

	for _, changed := range local {
		// Mark all the changed files as matching the new remote version. The
		// file contents were already updated above. This code can't be run until
		// after we pull the index again to get the updated remote versions.
		changed.File.VersionLocal = changed.File.VersionRemote
	}

	if err := m.Save(); err != nil {
		return err
	}

	fmt.Fprintln(cli.Stdout, "Push complete.")
	return nil
}
