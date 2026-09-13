package assets

import "embed"

//go:embed codex-skill/SKILL.md
var CodexSkill []byte

//go:embed docs/*.md
var Docs embed.FS
