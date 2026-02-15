package web

import "embed"

//go:embed tmux-adapter-web/* tmux-converter-web/* shared/*
var Files embed.FS
