// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestResourceSizeRequired(t *testing.T) {
	for _, test := range []struct {
		name string
		size string
		ok   bool
	}{
		{"missing", "", false},
		{"null", `,"size":null`, false},
		{"zero", `,"size":0`, true},
		{"positive", `,"size":1`, true},
		{"negative", `,"size":-1`, false},
		{"fraction", `,"size":0.5`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := `{"uri":"skill://demo/SKILL.md","frontmatter":{"name":"demo","description":"Demo"},"resources":[{"uri":"skill://demo/SKILL.md","digest":"sha256:` + strings.Repeat("0", 64) + `"` + test.size + `}]}`
			var skill Skill
			err := json.Unmarshal([]byte(data), &skill)
			if err == nil {
				err = ValidateSkill(&skill)
			}
			if (err == nil) != test.ok {
				t.Fatalf("decode and validate: %v, want success = %v", err, test.ok)
			}
		})
	}
}

func TestDynamicMarkerJSON(t *testing.T) {
	for _, data := range []string{`"dynamic"`, `"dyn\u0061mic"`, ` "\u0064ynamic" `} {
		var resources Resources
		if err := json.Unmarshal([]byte(data), &resources); err != nil || !resources.IsDynamic() {
			t.Errorf("decode %s = %+v, %v", data, resources, err)
		}
	}
	for _, data := range []string{`"other"`, `null`, `42`, `{}`} {
		var resources Resources
		if err := json.Unmarshal([]byte(data), &resources); err == nil {
			t.Errorf("decode %s succeeded", data)
		}
	}
}

func TestSkillNames(t *testing.T) {
	for _, test := range []struct {
		name string
		ok   bool
	}{
		{"demo", true}, {"café", true}, {"中文", true}, {"résumé-٢", true},
		{strings.Repeat("é", 64), true}, {strings.Repeat("é", 65), false},
		{"", false}, {"CAFÉ", false}, {"-demo", false}, {"demo-", false},
		{"demo--test", false}, {"demo_test", false}, {"demo/test", false},
		{"demo\xff", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			skill := &Skill{
				URI:         "skill://org/" + test.name + "/SKILL.md",
				Frontmatter: Frontmatter{"name": test.name, "description": "Demo"},
				Resources:   DynamicResources(),
			}
			if err := ValidateSkill(skill); (err == nil) != test.ok {
				t.Fatalf("ValidateSkill() = %v, want success = %v", err, test.ok)
			}
		})
	}
}

func TestVerifyFrontmatterNumbers(t *testing.T) {
	for _, test := range []struct {
		name, advertised, yaml string
		matches                bool
	}{
		{"large-integer", "9007199254740993", "9007199254740993", true},
		{"changed-large-integer", "9007199254740992", "9007199254740993", false},
		{"uint64", "18446744073709551615", "18446744073709551615", true},
		{"exponent", "9007199254740993e0", "9007199254740993", true},
		{"decimal", "1.00", "1", true},
		{"fraction", "0.00100", "0.001", true},
		{"negative", "-12.30", "-12.3", true},
		{"zero", "-0.00e1000000000", "0", true},
		{"large-exponent", "1e1000000000", "1", false},
		{"small-exponent", "1e-1000000000", "0", false},
		{"string-is-not-number", `"1"`, "1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := []byte("---\nname: demo\ndescription: Demo\nextra:\n  values: [" + test.yaml + "]\n---\nBody\n")
			data := `{"name":"demo","description":"Demo","extra":{"values":[` + test.advertised + `]}}`
			var fields Frontmatter
			if err := json.Unmarshal([]byte(data), &fields); err != nil {
				t.Fatal(err)
			}
			skill := &Skill{URI: "skill://demo/SKILL.md", Frontmatter: fields}
			for _, dynamic := range []bool{false, true} {
				skill.Resources = StaticResources(&Resource{
					URI: skill.URI, Size: int64(len(content)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(content)),
				})
				if dynamic {
					skill.Resources = DynamicResources()
				}
				err := VerifySkillMD(skill, content)
				matched := err == nil || dynamic && errors.Is(err, ErrDynamicResources)
				if matched != test.matches {
					t.Fatalf("dynamic=%v: VerifySkillMD() = %v, want match = %v", dynamic, err, test.matches)
				}
			}
		})
	}
}
