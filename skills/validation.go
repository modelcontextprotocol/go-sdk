// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func parseFrontmatter(data []byte) (Frontmatter, error) {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return nil, fmt.Errorf("SKILL.md must begin with YAML frontmatter")
	}
	end := bytes.Index(normalized[4:], []byte("\n---\n"))
	if end < 0 {
		return nil, fmt.Errorf("SKILL.md frontmatter has no closing delimiter")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(normalized[4:4+end], &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("SKILL.md frontmatter is empty")
	}
	frontmatter := Frontmatter(fields)
	for key, value := range frontmatter {
		normalized, err := normalizeYAML(value)
		if err != nil {
			return nil, fmt.Errorf("frontmatter field %q: %w", key, err)
		}
		frontmatter[key] = normalized
	}
	return frontmatter, nil
}

func normalizeYAML(value any) (any, error) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			normalized, err := normalizeYAML(item)
			if err != nil {
				return nil, err
			}
			value[key] = normalized
		}
		return value, nil
	case map[any]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("mapping key must be a string")
			}
			normalized, err := normalizeYAML(item)
			if err != nil {
				return nil, err
			}
			result[name] = normalized
		}
		return result, nil
	case []any:
		for i, item := range value {
			normalized, err := normalizeYAML(item)
			if err != nil {
				return nil, err
			}
			value[i] = normalized
		}
		return value, nil
	default:
		return value, nil
	}
}

const (
	// DefaultMaxResourcesPerSkill is the SEP-2640 per-skill resource limit.
	DefaultMaxResourcesPerSkill = 512
	// DefaultMaxTotalSize is the SEP-2640 per-skill byte limit.
	DefaultMaxTotalSize = 16 * 1024 * 1024
)

// Limits controls the limits applied to a static skill manifest.
type Limits struct {
	MaxResourcesPerSkill int
	MaxTotalSize         int64
}

var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// DefaultLimits returns the limits required by SEP-2640.
func DefaultLimits() Limits {
	return Limits{
		MaxResourcesPerSkill: DefaultMaxResourcesPerSkill,
		MaxTotalSize:         DefaultMaxTotalSize,
	}
}

// ValidateSkill validates a skill using the Agent Skills and SEP-2640 defaults.
func ValidateSkill(skill *Skill) error {
	return ValidateSkillWithLimits(skill, DefaultLimits())
}

// ValidateSkillWithLimits validates a skill using the supplied manifest limits.
func ValidateSkillWithLimits(skill *Skill, limits Limits) error {
	if skill == nil {
		return fmt.Errorf("skill is nil")
	}
	name, err := skillNameFromURI(skill.URI)
	if err != nil {
		return err
	}
	if skill.Frontmatter == nil {
		return fmt.Errorf("skill %q has no frontmatter", skill.URI)
	}
	if _, err := json.Marshal(skill.Frontmatter); err != nil {
		return fmt.Errorf("skill %q frontmatter is not JSON-compatible: %w", skill.URI, err)
	}
	frontmatterName, ok := skill.Frontmatter["name"].(string)
	if !ok {
		return fmt.Errorf("skill %q frontmatter name must be a string", skill.URI)
	}
	if err := validateName(frontmatterName); err != nil {
		return fmt.Errorf("skill %q: %w", skill.URI, err)
	}
	if frontmatterName != name {
		return fmt.Errorf("skill %q frontmatter name %q does not match URI name %q", skill.URI, frontmatterName, name)
	}
	description, ok := skill.Frontmatter["description"].(string)
	if !ok || utf8.RuneCountInString(description) < 1 || utf8.RuneCountInString(description) > 1024 {
		return fmt.Errorf("skill %q frontmatter description must contain 1 to 1024 characters", skill.URI)
	}
	if compatibility, ok := skill.Frontmatter["compatibility"]; ok {
		s, ok := compatibility.(string)
		if !ok || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 500 {
			return fmt.Errorf("skill %q frontmatter compatibility must contain 1 to 500 characters", skill.URI)
		}
	}
	if license, ok := skill.Frontmatter["license"]; ok {
		if _, ok := license.(string); !ok {
			return fmt.Errorf("skill %q frontmatter license must be a string", skill.URI)
		}
	}
	if metadata, ok := skill.Frontmatter["metadata"]; ok {
		var m map[string]any
		switch metadata := metadata.(type) {
		case map[string]any:
			m = metadata
		case map[string]string:
			m = make(map[string]any, len(metadata))
			for key, value := range metadata {
				m[key] = value
			}
		default:
			return fmt.Errorf("skill %q frontmatter metadata must be an object", skill.URI)
		}
		for key, value := range m {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("skill %q frontmatter metadata value %q must be a string", skill.URI, key)
			}
		}
	}
	if allowedTools, ok := skill.Frontmatter["allowed-tools"]; ok {
		if _, ok := allowedTools.(string); !ok {
			return fmt.Errorf("skill %q frontmatter allowed-tools must be a string", skill.URI)
		}
	}

	resources, static := skill.Resources.List()
	if skill.Resources.IsDynamic() {
		return nil
	}
	if !static {
		return fmt.Errorf("skill %q resources is not set", skill.URI)
	}
	if limits.MaxResourcesPerSkill > 0 && len(resources) > limits.MaxResourcesPerSkill {
		return fmt.Errorf("skill %q has %d resources, exceeding the limit of %d", skill.URI, len(resources), limits.MaxResourcesPerSkill)
	}
	seen := make(map[string]bool, len(resources))
	var total int64
	for i, resource := range resources {
		if resource == nil {
			return fmt.Errorf("skill %q resource %d is nil", skill.URI, i)
		}
		if err := validateResourceURI(skill.URI, resource.URI); err != nil {
			return fmt.Errorf("skill %q resource %q: %w", skill.URI, resource.URI, err)
		}
		if seen[resource.URI] {
			return fmt.Errorf("skill %q lists resource %q more than once", skill.URI, resource.URI)
		}
		seen[resource.URI] = true
		if !digestRE.MatchString(resource.Digest) {
			return fmt.Errorf("skill %q resource %q has invalid SHA-256 digest", skill.URI, resource.URI)
		}
		if resource.Size < 0 {
			return fmt.Errorf("skill %q resource %q has a negative size", skill.URI, resource.URI)
		}
		if resource.Size > math.MaxInt64-total {
			return fmt.Errorf("skill %q resource sizes overflow int64", skill.URI)
		}
		total += resource.Size
	}
	if !seen[skill.URI] {
		return fmt.Errorf("skill %q resources does not include its SKILL.md", skill.URI)
	}
	if limits.MaxTotalSize > 0 && total > limits.MaxTotalSize {
		return fmt.Errorf("skill %q has %d bytes, exceeding the limit of %d", skill.URI, total, limits.MaxTotalSize)
	}
	return nil
}

// ValidateDirectoryResult validates that result contains direct children of uri.
func ValidateDirectoryResult(uri string, result *ReadDirectoryResult) error {
	if result == nil {
		return fmt.Errorf("directory result is nil")
	}
	parent, err := parseDirectoryURI(uri)
	if err != nil {
		return err
	}
	if result.Resources == nil {
		return fmt.Errorf("directory %q returned a null resources array", uri)
	}
	seenNames := make(map[string]bool, len(result.Resources))
	seenURIs := make(map[string]bool, len(result.Resources))
	for i, resource := range result.Resources {
		if resource == nil {
			return fmt.Errorf("directory %q resource %d is nil", uri, i)
		}
		child, err := url.Parse(resource.URI)
		if err != nil || child.Scheme == "" {
			return fmt.Errorf("directory %q child has invalid URI %q", uri, resource.URI)
		}
		if child.Scheme != parent.Scheme || child.Host != parent.Host || child.RawQuery != "" || child.Fragment != "" {
			return fmt.Errorf("resource %q is not a child of directory %q", resource.URI, uri)
		}
		parentPath := strings.TrimSuffix(parent.Path, "/")
		childPath := child.Path
		prefix := parentPath + "/"
		if parentPath == "" {
			prefix = "/"
		}
		rel := strings.TrimPrefix(childPath, prefix)
		if rel == childPath || rel == "" || strings.Contains(rel, "/") {
			return fmt.Errorf("resource %q is not a direct child of directory %q", resource.URI, uri)
		}
		if strings.HasSuffix(resource.URI, "/") {
			return fmt.Errorf("resource %q has a trailing slash", resource.URI)
		}
		if seenNames[resource.Name] || seenURIs[resource.URI] {
			return fmt.Errorf("directory %q contains a duplicate child %q", uri, resource.URI)
		}
		seenNames[resource.Name] = true
		seenURIs[resource.URI] = true
	}
	return nil
}

func validateName(name string) error {
	if len(name) < 1 || len(name) > 64 || !skillNameRE.MatchString(name) {
		return fmt.Errorf("name %q must contain 1 to 64 lowercase ASCII letters, digits, or non-consecutive hyphens", name)
	}
	return nil
}

func skillNameFromURI(rawURI string) (string, error) {
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("skill URI %q is not a valid absolute resource URI", rawURI)
	}
	if u.Scheme == "skill" && (u.Host == "" || u.User != nil || u.Port() != "") {
		return "", fmt.Errorf("skill URI %q must use a host without userinfo or a port", rawURI)
	}
	if !strings.HasSuffix(u.Path, "/SKILL.md") {
		return "", fmt.Errorf("skill URI %q must end in /SKILL.md", rawURI)
	}
	dir := strings.Trim(strings.TrimSuffix(u.Path, "/SKILL.md"), "/")
	if dir == "" {
		dir = u.Hostname()
	} else {
		parts := strings.Split(dir, "/")
		dir = parts[len(parts)-1]
	}
	if dir == "" {
		return "", fmt.Errorf("skill URI %q has no skill name", rawURI)
	}
	return dir, nil
}

func validateResourceURI(skillURI, resourceURI string) error {
	skillURL, _ := url.Parse(skillURI)
	resourceURL, err := url.Parse(resourceURI)
	if err != nil || resourceURL.Scheme == "" || resourceURL.RawQuery != "" || resourceURL.Fragment != "" {
		return fmt.Errorf("invalid resource URI")
	}
	if resourceURL.Scheme == "skill" && (resourceURL.Host == "" || resourceURL.User != nil || resourceURL.Port() != "") {
		return fmt.Errorf("invalid skill resource authority")
	}
	if skillURL.Scheme != resourceURL.Scheme || skillURL.Host != resourceURL.Host {
		return fmt.Errorf("URI is outside the skill root")
	}
	rootPath := strings.TrimSuffix(skillURL.Path, "/SKILL.md")
	if resourceURL.Path != skillURL.Path && !strings.HasPrefix(resourceURL.Path, rootPath+"/") {
		return fmt.Errorf("URI is outside the skill root")
	}
	for _, segment := range strings.Split(resourceURL.Path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("URI contains a traversal segment")
		}
	}
	return nil
}

func parseDirectoryURI(rawURI string) (*url.URL, error) {
	if strings.HasSuffix(rawURI, "/") {
		return nil, fmt.Errorf("directory URI %q must not have a trailing slash", rawURI)
	}
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("directory URI %q is invalid", rawURI)
	}
	if u.Scheme == "skill" && (u.Host == "" || u.User != nil || u.Port() != "") {
		return nil, fmt.Errorf("directory URI %q has an invalid skill authority", rawURI)
	}
	return u, nil
}
