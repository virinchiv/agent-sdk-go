package types

import (
	"fmt"
	"strings"
	"time"
)

// DefaultA2ATimeout is the per-operation deadline applied when no timeout is configured.
// Shared between [github.com/agenticenv/agent-sdk-go/pkg/a2a/client.BuildConfig] and
// [github.com/agenticenv/agent-sdk-go/pkg/agent.agentConfigFingerprint].
const DefaultA2ATimeout = 30 * time.Second

// A2ASkillSpec describes one invocable skill advertised by an A2A agent.
// Used by the agent host to expose the remote skill as a Tool to the LLM.
// Canonical definition here; aliased in [github.com/agenticenv/agent-sdk-go/pkg/interfaces].
type A2ASkillSpec struct {
	// ID is the stable machine-readable identifier for the skill (used as tool name).
	ID string `json:"id"`
	// Name is the human-readable display name.
	Name string `json:"name,omitempty"`
	// Description explains when and how to invoke this skill; shown to the LLM.
	Description string `json:"description,omitempty"`
	// Tags are optional categorisation labels.
	Tags []string `json:"tags,omitempty"`
	// InputModes overrides the agent-level default input modes for this skill.
	InputModes []string `json:"inputModes,omitempty"`
	// OutputModes overrides the agent-level default output modes for this skill.
	OutputModes []string `json:"outputModes,omitempty"`
	// Examples are optional illustrative prompt strings shown to the LLM.
	Examples []string `json:"examples,omitempty"`
}

// A2ASkillFilter restricts which skills from [interfaces.A2AClient.ListSkills] are exposed as
// tools (exact skill-ID match). Set either AllowSkills or BlockSkills, not both.
// [A2ASkillFilter.Validate] checks mutual exclusivity (call from config build, e.g. [pkg/a2a/client.BuildConfig]).
// [A2ASkillFilter.Apply] filters skill specs and assumes Validate already passed for non-empty filters.
type A2ASkillFilter struct {
	// AllowSkills is an allow-list of skill IDs. Only listed skills are registered.
	AllowSkills []string
	// BlockSkills is a block-list of skill IDs. Listed skills are excluded.
	BlockSkills []string
}

// Validate returns an error if both AllowSkills and BlockSkills are set.
func (f A2ASkillFilter) Validate() error {
	if len(f.AllowSkills) > 0 && len(f.BlockSkills) > 0 {
		return fmt.Errorf("a2a skill filter: set either AllowSkills or BlockSkills, not both")
	}
	return nil
}

// Apply returns the subset of skills that pass the filter.
// When neither list is set every skill is returned unchanged.
// Assumes at most one of AllowSkills / BlockSkills is non-empty (i.e. [Validate] already passed).
func (f A2ASkillFilter) Apply(skills []A2ASkillSpec) []A2ASkillSpec {
	if len(f.AllowSkills) == 0 && len(f.BlockSkills) == 0 {
		return skills
	}
	var out []A2ASkillSpec
	if len(f.AllowSkills) > 0 {
		allowed := make(map[string]struct{}, len(f.AllowSkills))
		for _, id := range f.AllowSkills {
			allowed[strings.TrimSpace(id)] = struct{}{}
		}
		for _, s := range skills {
			if _, ok := allowed[strings.TrimSpace(s.ID)]; ok {
				out = append(out, s)
			}
		}
		return out
	}
	blocked := make(map[string]struct{}, len(f.BlockSkills))
	for _, id := range f.BlockSkills {
		blocked[strings.TrimSpace(id)] = struct{}{}
	}
	for _, s := range skills {
		if _, ok := blocked[strings.TrimSpace(s.ID)]; !ok {
			out = append(out, s)
		}
	}
	return out
}
