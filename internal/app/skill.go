package app

import (
	_ "embed"
)

// skillContent is the embedded SKILL.md for AI agent installation.
// Displayed via `deploi skill show`, installed via `deploi skill install`.
//
//go:embed SKILL.md
var skillContent string
