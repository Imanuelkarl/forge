// Package assets embeds the file templates goforge renders into generated projects.
package assets

import "embed"

// FS holds every template under templates/. Files use the .tmpl suffix so the Go
// toolchain never tries to compile them as part of goforge itself.
//
//go:embed all:templates
var FS embed.FS
