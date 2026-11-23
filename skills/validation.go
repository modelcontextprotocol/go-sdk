// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

func parseFrontmatter(data []byte) (Frontmatter, error) {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return nil, fmt.Errorf("SKILL.md must begin with YAML frontmatter")
	}
	end := bytes.Index(normalized[4:], []byte("\n---\n"))
	if end < 0 && bytes.HasSuffix(normalized, []byte("\n---")) {
		end = len(normalized) - 8
	}
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

// Limits bounds a static skill manifest. Positive fields are exact caps; zero
// fields are unlimited, and negative fields are invalid. The zero value imposes
// no manifest caps. Limits never disable structural validation.
//
// Limits are optional application policy. [BaselineLimits] provides the spec's
// interoperability baseline. Applications manage budgets for dynamic content.
type Limits struct {
	// MaxResourcesPerSkill limits the number of files, including SKILL.md.
	MaxResourcesPerSkill int
	// MaxTotalSize limits the sum of the files' raw byte lengths.
	MaxTotalSize int64
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// BaselineLimits returns the Skills spec's interoperability baseline: 512 files
// and 16 MiB per skill. Hosts must support at least this much and may support
// more; servers should stay within it for broad compatibility. The SDK does not
// impose these caps by default. Use explicit numeric limits to pin application
// policy independently of future spec revisions.
func BaselineLimits() Limits {
	return Limits{
		MaxResourcesPerSkill: 512,
		MaxTotalSize:         16 * 1024 * 1024,
	}
}

// ValidateSkill checks a skill's structure without imposing manifest caps.
func ValidateSkill(skill *Skill) error {
	return validateSkill(skill, Limits{})
}

// ValidateSkillWithLimits validates a skill using exactly the supplied limits.
// Zero fields impose no cap on that dimension; structural validation always runs.
func ValidateSkillWithLimits(skill *Skill, limits Limits) error {
	if err := limits.validate(); err != nil {
		return err
	}
	return validateSkill(skill, limits)
}

func (l Limits) validate() error {
	if l.MaxResourcesPerSkill < 0 || l.MaxTotalSize < 0 {
		return fmt.Errorf("skills: limits must not be negative")
	}
	return nil
}

func validateSkill(skill *Skill, limits Limits) error {
	if skill == nil {
		return fmt.Errorf("skill is nil")
	}
	name, skillURL, err := parseSkillURI(skill.URI)
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
	if frontmatterName != name {
		return fmt.Errorf("skill %q frontmatter name %q does not match URI name %q", skill.URI, frontmatterName, name)
	}
	description, ok := skill.Frontmatter["description"].(string)
	if length := utf8.RuneCountInString(description); !ok || length < 1 || length > 1024 {
		return fmt.Errorf("skill %q frontmatter description must contain 1 to 1024 characters", skill.URI)
	}
	if compatibility, ok := skill.Frontmatter["compatibility"]; ok {
		s, ok := compatibility.(string)
		if length := utf8.RuneCountInString(s); !ok || length < 1 || length > 500 {
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

	if skill.Resources.IsDynamic() {
		return nil
	}
	resources, static := skill.Resources.List()
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
		if err := validateResourceURI(skillURL, resource.URI); err != nil {
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
		if limits.MaxTotalSize > 0 {
			if resource.Size > limits.MaxTotalSize-total {
				return fmt.Errorf("skill %q resource sizes exceed the limit of %d bytes", skill.URI, limits.MaxTotalSize)
			}
			total += resource.Size
		}
	}
	if !seen[skill.URI] {
		return fmt.Errorf("skill %q resources does not include its SKILL.md", skill.URI)
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
	seenURIs := make(map[string]bool, len(result.Resources))
	for i, resource := range result.Resources {
		if resource == nil {
			return fmt.Errorf("directory %q resource %d is nil", uri, i)
		}
		child, err := parseURI(resource.URI)
		if err != nil {
			return fmt.Errorf("directory %q child has invalid URI %q", uri, resource.URI)
		}
		if child.Scheme != parent.Scheme || child.Host != parent.Host || child.User.String() != parent.User.String() {
			return fmt.Errorf("resource %q is not a child of directory %q", resource.URI, uri)
		}
		rel := strings.TrimPrefix(child.Path, parent.Path+"/")
		if rel == child.Path || rel == "" || strings.Contains(rel, "/") {
			return fmt.Errorf("resource %q is not a direct child of directory %q", resource.URI, uri)
		}
		if resource.Name == "" {
			return fmt.Errorf("directory %q child has no name", uri)
		}
		if seenURIs[resource.URI] {
			return fmt.Errorf("directory %q contains a duplicate child %q", uri, resource.URI)
		}
		seenURIs[resource.URI] = true
	}
	return nil
}

func validateName(name string) error {
	invalid := fmt.Errorf("name %q must contain 1 to 64 lowercase Unicode letters, numbers, or non-consecutive hyphens, with no leading or trailing hyphen", name)
	if length := utf8.RuneCountInString(name); length < 1 || length > 64 {
		return invalid
	}
	if name != strings.ToLower(name) {
		return invalid
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return invalid
	}
	// Invalid UTF-8 decodes to U+FFFD, which is neither a letter nor a number.
	for _, r := range name {
		if r != '-' && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return invalid
		}
	}
	return nil
}

func skillNameFromURI(rawURI string) (string, error) {
	name, _, err := parseSkillURI(rawURI)
	return name, err
}

// parseSkillURI validates a SKILL.md URI, returning the skill name and the
// parsed URI so that callers checking many resources parse the root only once.
func parseSkillURI(rawURI string) (string, *url.URL, error) {
	u, err := parseURI(rawURI)
	if err != nil {
		return "", nil, err
	}
	if !strings.HasSuffix(u.Path, "/SKILL.md") {
		return "", nil, fmt.Errorf("skill URI %q must end in /SKILL.md", rawURI)
	}
	dir := strings.TrimPrefix(strings.TrimSuffix(u.Path, "/SKILL.md"), "/")
	if dir == "" {
		dir = u.Hostname()
	} else {
		parts := strings.Split(dir, "/")
		dir = parts[len(parts)-1]
	}
	if dir == "" {
		return "", nil, fmt.Errorf("skill URI %q has no skill name", rawURI)
	}
	if err := validateName(dir); err != nil {
		return "", nil, err
	}
	return dir, u, nil
}

// validateResourceURI checks that resourceURI names a file under the skill root
// described by the already-parsed skillURL.
func validateResourceURI(skillURL *url.URL, resourceURI string) error {
	resourceURL, err := parseURI(resourceURI)
	if err != nil {
		return err
	}
	if skillURL.Scheme != resourceURL.Scheme || skillURL.Host != resourceURL.Host || skillURL.User.String() != resourceURL.User.String() {
		return fmt.Errorf("URI is outside the skill root")
	}
	rootPath := strings.TrimSuffix(skillURL.Path, "/SKILL.md")
	if !strings.HasPrefix(resourceURL.Path, rootPath+"/") || strings.HasSuffix(resourceURL.Path, "/") {
		return fmt.Errorf("URI is outside the skill root or is not a file")
	}
	return nil
}

func parseDirectoryURI(rawURI string) (*url.URL, error) {
	u, err := parseURI(rawURI)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(u.Path, "/") {
		return nil, fmt.Errorf("directory URI %q must not have a trailing slash", rawURI)
	}
	return u, nil
}

func parseURI(rawURI string) (*url.URL, error) {
	u, err := url.Parse(rawURI)
	// A non-empty fragment always leaves a "#" in the raw URI, so the raw check
	// covers both a parsed fragment and an empty one.
	if err != nil || u.Scheme == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(rawURI, "#") {
		return nil, fmt.Errorf("invalid resource URI %q", rawURI)
	}
	if u.Scheme == "skill" && (u.Host == "" || u.User != nil || u.Port() != "") {
		return nil, fmt.Errorf("invalid skill authority in %q", rawURI)
	}
	for segment := range strings.SplitSeq(u.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, fmt.Errorf("URI %q contains a traversal segment", rawURI)
		}
	}
	return u, nil
}

func validateListResult(result *ListSkillsResult, limits Limits) error {
	if result == nil || result.Skills == nil {
		return fmt.Errorf("skills is missing or null")
	}
	seen := make(map[string]bool, len(result.Skills))
	for _, skill := range result.Skills {
		if err := validateSkill(skill, limits); err != nil {
			return err
		}
		if seen[skill.URI] {
			return fmt.Errorf("skill URI %q occurs more than once", skill.URI)
		}
		seen[skill.URI] = true
	}
	return nil
}

func validateGetResult(uri string, result *GetSkillResult, limits Limits) error {
	if result == nil || result.Skill == nil {
		return fmt.Errorf("skill is missing or null")
	}
	if result.Skill.URI != uri {
		return fmt.Errorf("returned URI %q for %q", result.Skill.URI, uri)
	}
	return validateSkill(result.Skill, limits)
}

func validateCache(cache mcp.Cacheable) error {
	if cache.TTLMs < 0 {
		return fmt.Errorf("skills: ttlMs must not be negative")
	}
	if cache.CacheScope != cacheScopePublic && cache.CacheScope != cacheScopePrivate {
		return fmt.Errorf("skills: invalid cacheScope %q", cache.CacheScope)
	}
	return nil
}
