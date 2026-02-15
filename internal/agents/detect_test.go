package agents

import "testing"

func TestParseSessionName(t *testing.T) {
	tests := []struct {
		name     string
		wantRole string
		wantRig  string
	}{
		// Town-level (hq-*)
		{"hq-witness", "witness", ""},
		{"hq-overseer", "overseer", ""},

		// Rig-level known roles
		{"gt-myrig-witness", "witness", "myrig"},
		{"gt-myrig-refinery", "refinery", "myrig"},
		{"gt-myrig-overseer", "overseer", "myrig"},

		// Rig-level crew
		{"gt-myrig-crew", "crew", "myrig"},

		// Rig-level polecat (unknown role falls through to polecat)
		{"gt-myrig-bob", "polecat", "myrig"},

		// Boot
		{"gt-boot", "boot", ""},

		// Short gt prefix — only one part after split
		{"gt-x", "unknown", ""},

		// Project-scoped: PROJECT/ROLE/NAME
		{"hello_gastown/crew/bob", "crew", "hello_gastown"},

		// Unknown session name
		{"random-session", "unknown", ""},
	}

	for _, tt := range tests {
		role, rig := ParseSessionName(tt.name)
		if role != tt.wantRole || rig != tt.wantRig {
			t.Fatalf("ParseSessionName(%q) = (%q, %q), want (%q, %q)",
				tt.name, role, rig, tt.wantRole, tt.wantRig)
		}
	}
}

func TestGetProcessNames(t *testing.T) {
	tests := []struct {
		agent string
		want  []string
	}{
		{"claude", []string{"node", "claude"}},
		{"gemini", []string{"gemini"}},
		{"unknown-agent", []string{"node", "claude"}},
		{"", []string{"node", "claude"}},
	}

	for _, tt := range tests {
		got := GetProcessNames(tt.agent)
		if len(got) != len(tt.want) {
			t.Fatalf("GetProcessNames(%q) = %v, want %v", tt.agent, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("GetProcessNames(%q) = %v, want %v", tt.agent, got, tt.want)
			}
		}
	}
}

func TestIsAgentProcess(t *testing.T) {
	tests := []struct {
		command string
		names   []string
		want    bool
	}{
		{"node", []string{"node", "claude"}, true},
		{"claude", []string{"node", "claude"}, true},
		{"python", []string{"node", "claude"}, false},
		{"", []string{"node", "claude"}, false},
	}

	for _, tt := range tests {
		got := IsAgentProcess(tt.command, tt.names)
		if got != tt.want {
			t.Fatalf("IsAgentProcess(%q, %v) = %v, want %v",
				tt.command, tt.names, got, tt.want)
		}
	}
}

func TestIsShell(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"bash", true},
		{"zsh", true},
		{"sh", true},
		{"fish", true},
		{"tcsh", true},
		{"ksh", true},
		{"node", false},
		{"claude", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsShell(tt.command)
		if got != tt.want {
			t.Fatalf("IsShell(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestIsGastownSession(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"hq-witness", true},
		{"gt-myrig-crew", true},
		{"project/role/name", true},
		{"random", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsGastownSession(tt.name)
		if got != tt.want {
			t.Fatalf("IsGastownSession(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
