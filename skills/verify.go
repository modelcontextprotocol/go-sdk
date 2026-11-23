// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrDynamicResources reports that content cannot be integrity-verified because
// the skill declares dynamic resources.
var ErrDynamicResources = errors.New("skills: dynamic resources cannot be integrity-verified")

// VerifyResource checks content against a resource in the held skill entry.
func VerifyResource(skill *Skill, uri string, content []byte) error {
	if err := ValidateSkillWithLimits(skill, Limits{}); err != nil {
		return err
	}
	if err := validateResourceURI(skill.URI, uri); err != nil {
		return err
	}
	if skill.Resources.IsDynamic() {
		return ErrDynamicResources
	}
	resources, _ := skill.Resources.List()
	for _, resource := range resources {
		if resource.URI != uri {
			continue
		}
		if int64(len(content)) != resource.Size {
			return fmt.Errorf("skills: resource %q has size %d, expected %d", uri, len(content), resource.Size)
		}
		digest := sha256.Sum256(content)
		got := fmt.Sprintf("sha256:%x", digest)
		if got != resource.Digest {
			return fmt.Errorf("skills: resource %q has digest %q, expected %q", uri, got, resource.Digest)
		}
		return nil
	}
	return fmt.Errorf("skills: resource %q is not in the held skill manifest", uri)
}

// VerifySkillMD verifies both the content digest and the advertised frontmatter.
func VerifySkillMD(skill *Skill, content []byte) error {
	if err := VerifyResource(skill, skill.URI, content); err != nil {
		return err
	}
	frontmatter, err := parseFrontmatter(content)
	if err != nil {
		return err
	}
	want, err := json.Marshal(skill.Frontmatter)
	if err != nil {
		return fmt.Errorf("skills: marshaling listed frontmatter: %w", err)
	}
	got, err := json.Marshal(frontmatter)
	if err != nil {
		return fmt.Errorf("skills: marshaling resource frontmatter: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("skills: SKILL.md frontmatter does not match the skill entry")
	}
	return nil
}
