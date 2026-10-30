// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"fmt"
	"math"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// oversized returns a skill that exceeds exactly one baseline dimension.
func oversized(kind string) *Skill {
	return skillWith(func(s *Skill) {
		entries, _ := s.Resources.List()
		if kind == "count" {
			for i := range BaselineLimits().MaxResourcesPerSkill {
				entries = append(entries, &Resource{URI: fmt.Sprintf("skill://demo/%d.txt", i), Digest: testDigest, Size: 1})
			}
		} else {
			entries[0].Size = BaselineLimits().MaxTotalSize + 1
		}
		s.Resources = StaticResources(entries...)
	})
}

// checkLimitCalls exercises every client entry point that validates a manifest.
func checkLimitCalls(t *testing.T, client *mcp.ClientSession, skill *Skill, wantOK bool) {
	t.Helper()
	_, listErr := List(t.Context(), client, nil)
	_, getErr := Get(t.Context(), client, &GetSkillParams{URI: skill.URI})
	var allErr error
	count := 0
	for _, err := range All(t.Context(), client, nil) {
		if err != nil {
			allErr = err
			break
		}
		count++
	}
	for method, err := range map[string]error{"List": listErr, "Get": getErr, "All": allErr} {
		if (err == nil) != wantOK {
			t.Errorf("%s: error = %v, want success = %v", method, err, wantOK)
		}
	}
	if wantOK && count != 1 {
		t.Errorf("All yielded %d skills, want 1", count)
	}
}

// TestValidateSkillWithLimits covers the limit matrix by calling validation
// directly. Limits are SDK policy rather than protocol, so the conformance suite
// cannot observe them; TestServerLimits checks that requests reach this code.
func TestValidateSkillWithLimits(t *testing.T) {
	baseline := BaselineLimits()
	for _, kind := range []string{"count", "bytes"} {
		skill := oversized(kind)
		for _, test := range []struct {
			name   string
			limits Limits
			wantOK bool
		}{
			{"zero value imposes no caps", Limits{}, true},
			{"baseline", baseline, false},
			{"count only", Limits{MaxResourcesPerSkill: baseline.MaxResourcesPerSkill}, kind == "bytes"},
			{"bytes only", Limits{MaxTotalSize: baseline.MaxTotalSize}, kind == "count"},
			{"raised count", Limits{MaxResourcesPerSkill: baseline.MaxResourcesPerSkill + 1}, true},
			{"raised bytes", Limits{MaxTotalSize: baseline.MaxTotalSize + 1}, true},
			{"negative count", Limits{MaxResourcesPerSkill: -1}, false},
			{"negative bytes", Limits{MaxTotalSize: -1}, false},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				if err := ValidateSkillWithLimits(skill, test.limits); (err == nil) != test.wantOK {
					t.Fatalf("ValidateSkillWithLimits() = %v, want success = %v", err, test.wantOK)
				}
			})
		}
		if err := ValidateSkill(skill); err != nil {
			t.Errorf("%s: ValidateSkill imposed a manifest cap: %v", kind, err)
		}
	}

	for _, test := range []struct {
		name   string
		skill  *Skill
		limits Limits
		wantOK bool
	}{
		// Structural validation runs whatever the limits are.
		{"unlimited still validates structure", skillWith(func(s *Skill) { s.Frontmatter["name"] = "BAD" }), Limits{}, false},
		// A dynamic manifest has no countable resources, so caps do not apply.
		{"dynamic is exempt", skillWith(func(s *Skill) { s.Resources = DynamicResources() }), Limits{MaxResourcesPerSkill: 1, MaxTotalSize: 1}, true},
		// Sizes are compared against the remaining budget so the sum cannot overflow.
		{"total size cannot overflow", skillWith(func(s *Skill) {
			s.Resources = StaticResources(
				&Resource{URI: "skill://demo/SKILL.md", Digest: testDigest, Size: math.MaxInt64},
				&Resource{URI: "skill://demo/helper.txt", Digest: testDigest, Size: 1})
		}), Limits{MaxTotalSize: math.MaxInt64}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSkillWithLimits(test.skill, test.limits); (err == nil) != test.wantOK {
				t.Fatalf("ValidateSkillWithLimits() = %v, want success = %v", err, test.wantOK)
			}
		})
	}
}

func TestServerLimits(t *testing.T) {
	for _, kind := range []string{"count", "bytes"} {
		for _, limited := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/limited=%v", kind, limited), func(t *testing.T) {
				server := testServer()
				var options *ServerOptions
				if limited {
					options = &ServerOptions{Limits: BaselineLimits()}
				}
				skill := oversized(kind)
				if err := AddHandlers(server, fixedHandlers(skill), options); err != nil {
					t.Fatal(err)
				}
				checkLimitCalls(t, connectSkills(t, server, protocolVersionCaching), skill, !limited)
			})
		}
	}
	for _, limits := range []Limits{{MaxResourcesPerSkill: -1}, {MaxTotalSize: -1}} {
		if err := AddHandlers(testServer(), fixedHandlers(testSkill()), &ServerOptions{Limits: limits}); err == nil {
			t.Fatal("accepted negative server limits")
		}
	}
}

func TestServerLimitOwnership(t *testing.T) {
	server := testServer()
	options := &ServerOptions{Limits: BaselineLimits()}
	skill := testSkill()
	if err := AddHandlers(server, fixedHandlers(skill), options); err != nil {
		t.Fatal(err)
	}
	options.Limits = Limits{MaxTotalSize: -1}
	checkLimitCalls(t, connectSkills(t, server, protocolVersionCaching), skill, true)
}

func TestClientSupportsBaselineAndLargerSkills(t *testing.T) {
	for _, version := range []string{"2025-11-25", protocolVersionCaching} {
		for _, kind := range []string{"count", "bytes"} {
			for _, above := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/above=%v", version, kind, above), func(t *testing.T) {
					skill := oversized(kind)
					if !above {
						entries, _ := skill.Resources.List()
						if kind == "count" {
							skill.Resources = StaticResources(entries[:512]...)
						} else {
							entries[0].Size = 16 << 20
						}
					}
					server := testServer()
					if err := AddHandlers(server, fixedHandlers(skill), nil); err != nil {
						t.Fatal(err)
					}
					checkLimitCalls(t, connectSkills(t, server, version), skill, true)
				})
			}
		}
	}
}
