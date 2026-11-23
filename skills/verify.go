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

// VerifyResource checks membership, size, and digest against the held skill entry.
// It returns [ErrDynamicResources] for a dynamic manifest. It checks the entry's
// structure without reapplying size limits configured during discovery.
func VerifyResource(skill *Skill, uri string, content []byte) error {
	// Content verification must not reimpose default limits on an accepted entry.
	if err := validateSkill(skill, Limits{}); err != nil {
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

// VerifySkillMD verifies SKILL.md with [VerifyResource] and compares every
// frontmatter field with the held entry. For a dynamic manifest it still checks
// frontmatter, returning [ErrDynamicResources] only if the frontmatter matches.
func VerifySkillMD(skill *Skill, content []byte) error {
	if skill == nil {
		return fmt.Errorf("skills: nil skill")
	}
	verificationErr := VerifyResource(skill, skill.URI, content)
	if verificationErr != nil && !errors.Is(verificationErr, ErrDynamicResources) {
		return verificationErr
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
	return verificationErr
}
