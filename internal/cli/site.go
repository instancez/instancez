package cli

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

const (
	maxSiteFiles = 5000
	maxSiteBytes = 100 << 20 // 100 MiB uncompressed
	siteEnvVar   = "INSTANCEZ_SITE"
	siteFile     = ".instancez/site"
	ignoreFile   = ".instancezignore"
)

var archiveHook func(path string)

type siteLimits struct {
	files int
	bytes int64
}

func newSiteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "site", Short: "Manage static sites"}
	cmd.AddCommand(newSiteDeployCmd(), newSiteListCmd())
	return cmd
}

func newSiteDeployCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Upload a prebuilt static bundle to a site",
		Long: `Archive a built static bundle (default ./dist) as tar.gz and deploy it to a site.

The site is a site id or slug, from --site, else $INSTANCEZ_SITE, else the first
line of ./.instancez/site. index.html must be at the root of the directory and
non-empty. A symlinked <dir> is followed; symlinks inside it are rejected.

<dir>/.instancezignore lists patterns to skip, one per line: path.Match globs
(*, ?, [a-z]) matched against the relative path or the file name, a trailing /
for directories, # for comments. Negation (!), ** and a leading / are errors.
.git, node_modules, .DS_Store and .instancezignore itself are always skipped.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "dist"
			if len(args) == 1 {
				dir = args[0]
			}
			ref, err := resolveSiteRef(site, os.Getenv(siteEnvVar), siteFile)
			if err != nil {
				return err
			}
			return runSiteDeploy(cmd.OutOrStdout(), dir, ref)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site id or slug (else $INSTANCEZ_SITE, else ./.instancez/site)")
	return cmd
}

// resolveSiteRef picks the site reference: flag, then env, then file.
func resolveSiteRef(flag, env, file string) (string, error) {
	for _, v := range []string{flag, env} {
		if v = strings.TrimSpace(v); v != "" {
			return v, nil
		}
	}
	if b, err := os.ReadFile(file); err == nil {
		line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("no site given: pass --site <id|slug>, set %s, or put the site id or slug in %s", siteEnvVar, siteFile)
}

func runSiteDeploy(out io.Writer, dir, ref string) error {
	creds, err := ensureLoggedIn(ensureLoginOpts{})
	if err != nil {
		return err
	}
	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	site, err := c.ResolveSite(ref)
	if err != nil {
		return err
	}

	arc, n, err := buildSiteArchive(dir, siteLimits{maxSiteFiles, maxSiteBytes})
	if err != nil {
		return err
	}
	defer func() { _ = arc.Close(); _ = os.Remove(arc.Name()) }()
	st, err := arc.Stat()
	if err != nil {
		return err
	}

	up, err := c.MintSiteUpload(site.ID)
	if err != nil {
		return err
	}
	res, err := c.UploadSiteArchive(up.UploadURL, arc, st.Size())
	if err != nil {
		return err
	}
	url := res.URL
	if url == "" {
		url = site.URL
	}
	_, _ = fmt.Fprintf(out, "  ✓ Deployed %d file(s) from %s to %s %s\n", n, dir, site.Slug, url)
	return nil
}

type ignoreRules struct{ patterns []string }

// validateIgnorePattern accepts only path.Match globs with an optional
// trailing "/" for directories.
func validateIgnorePattern(p string) error {
	switch {
	case strings.HasPrefix(p, "!"):
		return errors.New("negation (!) is not supported")
	case strings.HasPrefix(p, "/"):
		return errors.New("a leading / is not supported")
	case strings.Contains(p, "**"):
		return errors.New("** is not supported")
	}
	if _, err := path.Match(strings.TrimSuffix(p, "/"), ""); err != nil {
		return fmt.Errorf("malformed glob: %w", err)
	}
	return nil
}

func loadIgnore(dir string) (ignoreRules, error) {
	r := ignoreRules{patterns: []string{".git/", "node_modules/", ".DS_Store", ignoreFile}}
	b, err := os.ReadFile(filepath.Join(dir, ignoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("read %s: %w", ignoreFile, err)
	}
	for i, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if err := validateIgnorePattern(l); err != nil {
			return r, fmt.Errorf("%s line %d (%q): %w", ignoreFile, i+1, l, err)
		}
		r.patterns = append(r.patterns, l)
	}
	return r, nil
}

// match reports whether the slash-separated relative path is ignored.
func (r ignoreRules) match(rel string, isDir bool) bool {
	for _, p := range r.patterns {
		dirOnly := strings.HasSuffix(p, "/")
		p = strings.Trim(p, "/")
		if p == "" || (dirOnly && !isDir) {
			continue
		}
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if !strings.Contains(p, "/") {
			if ok, _ := path.Match(p, path.Base(rel)); ok {
				return true
			}
		}
	}
	return false
}

// buildSiteArchive writes dir into a temp tar.gz (paths relative to dir) and
// returns it rewound, with the file count. The caller closes and removes it.
func buildSiteArchive(dir string, lim siteLimits) (*os.File, int, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, 0, fmt.Errorf("%s is not a directory", dir)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", dir, err)
	}
	if ri, err := os.Stat(root); err != nil || !ri.IsDir() {
		return nil, 0, fmt.Errorf("%s is not a directory", dir)
	}
	dir = root
	rules, err := loadIgnore(dir)
	if err != nil {
		return nil, 0, err
	}

	f, err := os.CreateTemp("", "inz-site-*.tgz")
	if err != nil {
		return nil, 0, err
	}
	fail := func(err error) (*os.File, int, error) {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, 0, err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	var count int
	var total int64
	hasIndex := false
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if rules.match(slash, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; replace it with the real file", slash)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", slash)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if count >= lim.files {
			return fmt.Errorf("too many files (> %d)", lim.files)
		}
		total += fi.Size()
		if total > lim.bytes {
			return fmt.Errorf("bundle too large (> %d bytes)", lim.bytes)
		}
		if slash == "index.html" && fi.Size() == 0 {
			return errors.New("index.html is empty")
		}
		if archiveHook != nil {
			archiveHook(p)
		}
		hdr := &tar.Header{Name: slash, Mode: 0o644, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, src)
		_ = src.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", slash, err)
		}
		if n != fi.Size() {
			return fmt.Errorf("%s changed while archiving", slash)
		}
		count++
		if slash == "index.html" {
			hasIndex = true
		}
		return nil
	})
	if walkErr != nil {
		return fail(walkErr)
	}
	if count == 0 {
		return fail(fmt.Errorf("%s has no files to deploy", dir))
	}
	if !hasIndex {
		return fail(fmt.Errorf("%s has no index.html at its root", dir))
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return f, count, nil
}

func newSiteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List static sites with their ids and slugs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds, err := ensureLoggedIn(ensureLoginOpts{})
			if err != nil {
				return err
			}
			sites, err := cloud.NewClient(cloud.APIURL(), creds.PAT).ListSites()
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No sites yet.")
				return nil
			}
			for _, s := range sites {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", s.ID, s.Slug, s.URL)
			}
			return nil
		},
	}
}
