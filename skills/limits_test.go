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

func TestLimitsRoundTrip(t *testing.T) {
	baseline := BaselineLimits()
	for _, kind := range []string{"count", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			skill := testSkill()
			entries, _ := skill.Resources.List()
			if kind == "count" {
				for i := 1; i <= baseline.MaxResourcesPerSkill; i++ {
					entries = append(entries, &Resource{URI: fmt.Sprintf("skill://demo/%d.txt", i), Digest: entries[0].Digest, Size: 1})
				}
			} else {
				entries[0].Size = baseline.MaxTotalSize + 1
			}
			skill.Resources = StaticResources(entries...)
			for _, test := range []struct {
				name   string
				limits Limits
				wantOK bool
			}{
				{"zero-value", Limits{}, true},
				{"baseline", baseline, false},
				{"count-only", Limits{MaxResourcesPerSkill: baseline.MaxResourcesPerSkill}, kind == "bytes"},
				{"bytes-only", Limits{MaxTotalSize: baseline.MaxTotalSize}, kind == "count"},
				{"raised-count", Limits{MaxResourcesPerSkill: baseline.MaxResourcesPerSkill + 1}, true},
				{"raised-bytes", Limits{MaxTotalSize: baseline.MaxTotalSize + 1}, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Run("client", func(t *testing.T) {
						server := testServer()
						if err := AddHandlers(server, fixedHandlers(skill), nil); err != nil {
							t.Fatal(err)
						}
						client := connectSkills(t, server, "2026-07-28")
						client.Limits = test.limits
						checkLimitCalls(t, client, skill, test.wantOK)
					})
					t.Run("server", func(t *testing.T) {
						server := testServer()
						if err := AddHandlers(server, fixedHandlers(skill), &ServerOptions{Limits: test.limits}); err != nil {
							t.Fatal(err)
						}
						client := connectSkills(t, server, "2026-07-28")
						client.Limits = Limits{}
						checkLimitCalls(t, client, skill, test.wantOK)
					})
				})
			}
			if err := ValidateSkill(skill); err != nil {
				t.Fatalf("ValidateSkill imposed a manifest cap: %v", err)
			}
			if err := ValidateSkillWithLimits(skill, baseline); err == nil {
				t.Fatal("baseline validation accepted an oversized manifest")
			}
		})
	}
}

func TestNegativeLimits(t *testing.T) {
	server := testServer()
	skill := testSkill()
	handlers := fixedHandlers(skill)
	var calls atomic.Int32
	list, get := handlers.List, handlers.Get
	handlers.List = func(ctx context.Context, session *mcp.ServerSession, params *ListSkillsParams) (*ListSkillsResult, error) {
		calls.Add(1)
		return list(ctx, session, params)
	}
	handlers.Get = func(ctx context.Context, session *mcp.ServerSession, params *GetSkillParams) (*GetSkillResult, error) {
		calls.Add(1)
		return get(ctx, session, params)
	}
	if err := AddHandlers(server, handlers, nil); err != nil {
		t.Fatal(err)
	}
	client := connectSkills(t, server, "2026-07-28")
	for _, limits := range []Limits{{MaxResourcesPerSkill: -1}, {MaxTotalSize: -1}} {
		if err := ValidateSkillWithLimits(skill, limits); err == nil {
			t.Fatal("negative validation limit accepted")
		}
		if err := AddHandlers(testServer(), handlers, &ServerOptions{Limits: limits}); err == nil {
			t.Fatal("negative server limit accepted")
		}
		client.Limits = limits
		checkLimitCalls(t, client, skill, false)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid client limits sent %d requests", got)
	}
}

func TestLimitOwnership(t *testing.T) {
	skill := testSkill()
	entries, _ := skill.Resources.List()
	skill.Resources = StaticResources(entries[0], &Resource{URI: "skill://demo/helper.txt", Digest: entries[0].Digest, Size: 1})
	server := testServer()
	options := &ServerOptions{Limits: Limits{MaxResourcesPerSkill: 2}}
	if err := AddHandlers(server, fixedHandlers(skill), options); err != nil {
		t.Fatal(err)
	}
	options.Limits = Limits{MaxTotalSize: 1}
	client := connectSkills(t, server, "2026-07-28")
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

func TestUnlimitedLimitsKeepStructuralValidation(t *testing.T) {
	skill := testSkill()
	skill.Frontmatter["name"] = "BAD"
	if err := ValidateSkillWithLimits(skill, Limits{}); err == nil {
		t.Fatal("unlimited validation accepted malformed frontmatter")
	}
	server := testServer()
	if err := AddHandlers(server, fixedHandlers(skill), nil); err != nil {
		t.Fatal(err)
	}
	client := connectSkills(t, server, "2026-07-28")
	client.Limits = Limits{}
	checkLimitCalls(t, client, skill, false)
}

func TestUnlimitedTotalSize(t *testing.T) {
	skill := testSkill()
	entries, _ := skill.Resources.List()
	entries[0].Size = math.MaxInt64
	skill.Resources = StaticResources(entries[0], &Resource{
		URI: "skill://demo/helper.txt", Digest: entries[0].Digest, Size: 1,
	})
	if err := ValidateSkill(skill); err != nil {
		t.Fatalf("unlimited validation accumulated a total size: %v", err)
	}
	if err := ValidateSkillWithLimits(skill, Limits{MaxTotalSize: math.MaxInt64}); err == nil {
		t.Fatal("total size overflow bypassed the configured cap")
	}
}

func TestDynamicLimits(t *testing.T) {
	skill := testSkill()
	skill.Resources = DynamicResources()
	limits := Limits{MaxResourcesPerSkill: 1, MaxTotalSize: 1}
	server := testServer()
	if err := AddHandlers(server, fixedHandlers(skill), &ServerOptions{Limits: limits}); err != nil {
		t.Fatal(err)
	}
	client := connectSkills(t, server, "2026-07-28")
	client.Limits = limits
	checkLimitCalls(t, client, skill, true)
}
