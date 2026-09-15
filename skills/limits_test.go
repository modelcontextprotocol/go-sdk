// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
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
func checkLimitCalls(t *testing.T, client *Client, skill *Skill, wantOK bool) {
	t.Helper()
	_, listErr := client.List(t.Context(), nil)
	_, getErr := client.Get(t.Context(), &GetSkillParams{URI: skill.URI})
	var allErr error
	count := 0
	for _, err := range client.All(t.Context(), nil) {
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
// cannot observe them; TestLimitsArePlumbed checks that requests reach this code.
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

// TestLimitsArePlumbed checks that Client.Limits and ServerOptions.Limits reach
// validation over a connection, and that invalid limits fail before a request is
// sent. The matrix itself lives in TestValidateSkillWithLimits.
func TestLimitsArePlumbed(t *testing.T) {
	skill := oversized("count")
	var calls atomic.Int32
	counted := func(skill *Skill) *Handlers {
		h := fixedHandlers(skill)
		list, get := h.List, h.Get
		h.List = func(ctx context.Context, s *mcp.ServerSession, p *ListSkillsParams) (*ListSkillsResult, error) {
			calls.Add(1)
			return list(ctx, s, p)
		}
		h.Get = func(ctx context.Context, s *mcp.ServerSession, p *GetSkillParams) (*GetSkillResult, error) {
			calls.Add(1)
			return get(ctx, s, p)
		}
		return h
	}

	for _, side := range []string{"client", "server"} {
		for _, test := range []struct {
			name   string
			limits Limits
			wantOK bool
		}{
			{"unlimited", Limits{}, true},
			{"baseline", BaselineLimits(), false},
		} {
			t.Run(side+"/"+test.name, func(t *testing.T) {
				server := testServer()
				var options *ServerOptions
				if side == "server" {
					options = &ServerOptions{Limits: test.limits}
				}
				if err := AddHandlers(server, fixedHandlers(skill), options); err != nil {
					t.Fatal(err)
				}
				client := connectSkills(t, server, protocolVersionCaching)
				if side == "client" {
					client.Limits = test.limits
				}
				checkLimitCalls(t, client, skill, test.wantOK)
			})
		}
	}

	t.Run("negative limits fail before sending", func(t *testing.T) {
		valid := testSkill()
		server := testServer()
		if err := AddHandlers(server, counted(valid), nil); err != nil {
			t.Fatal(err)
		}
		client := connectSkills(t, server, protocolVersionCaching)
		for _, limits := range []Limits{{MaxResourcesPerSkill: -1}, {MaxTotalSize: -1}} {
			if err := AddHandlers(testServer(), counted(valid), &ServerOptions{Limits: limits}); err == nil {
				t.Error("AddHandlers accepted a negative limit")
			}
			client.Limits = limits
			checkLimitCalls(t, client, valid, false)
		}
		if got := calls.Load(); got != 0 {
			t.Fatalf("invalid client limits sent %d requests", got)
		}
	})
}

// TestLimitOwnership checks that limits are captured at registration and at
// iterator creation, so later mutation of the caller's value has no effect.
func TestLimitOwnership(t *testing.T) {
	skill := skillWith(func(s *Skill) {
		s.Resources = StaticResources(
			&Resource{URI: "skill://demo/SKILL.md", Digest: testDigest, Size: 1},
			&Resource{URI: "skill://demo/helper.txt", Digest: testDigest, Size: 1})
	})
	server := testServer()
	options := &ServerOptions{Limits: Limits{MaxResourcesPerSkill: 2}}
	if err := AddHandlers(server, fixedHandlers(skill), options); err != nil {
		t.Fatal(err)
	}
	options.Limits = Limits{MaxTotalSize: 1} // must not affect the registered handlers

	client := connectSkills(t, server, protocolVersionCaching)
	client.Limits = Limits{MaxResourcesPerSkill: 2}
	checkLimitCalls(t, client, skill, true)

	seq := client.All(t.Context(), nil)
	client.Limits.MaxResourcesPerSkill = 1
	checkLimitCalls(t, client, skill, false)
	for range 2 {
		count := 0
		for _, err := range seq {
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("captured iterator yielded %d skills, want 1", count)
		}
	}
}
