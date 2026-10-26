//go:build !dev

package gui

import (
	"io/fs"
	"net/http"
)

// The shipped app serves the UI from the binary, in one process.
func staticFS() fs.FS { f, _ := fs.Sub(assets, "assets"); return f }

// assetsFromDisk is false in a shipped build: the page's files are the
// binary's, so a reload re-runs them and a changed file needs a new build.
func assetsFromDisk() bool                        { return false }
func devRoutes(*http.ServeMux)                    {}
func devListen(http.Handler)                      {}
func devPage(h http.Handler) http.Handler         { return h }
func devRole() string                             { return "" }
func devBackend(func(Windows) http.Handler) error { return nil }
func stash(link string) string                    { return stashImport(link) }
