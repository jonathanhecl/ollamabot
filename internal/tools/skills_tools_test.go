package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSkillAndWriteValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test case 1: CreateSkill succeeds with valid parameters
	err = CreateSkill(tmpDir, "valid-skill", "A valid skill description", "http://homepage.com", "- Instruction 1\n- Instruction 2")
	if err != nil {
		t.Errorf("expected CreateSkill to succeed, got: %v", err)
	}

	// Test case 2: CreateSkill fails due to validation (empty instructions)
	err = CreateSkill(tmpDir, "invalid-skill", "A description", "http://homepage.com", "")
	if err == nil {
		t.Error("expected CreateSkill to fail with empty instructions, but it succeeded")
	}

	// Test case 3: WriteFile with "SKILL.md" validation
	wsDir := filepath.Join(tmpDir, "ws")
	_ = os.MkdirAll(wsDir, 0755)

	// 3a. Valid skill file write should succeed
	validSkillMD := `---
name: test-skill
description: some desc
homepage: http://test.com
---

## Description
some desc

## Instructions
- [ ] Do something
`
	err = WriteFile(wsDir, "my-skill/SKILL.md", validSkillMD)
	if err != nil {
		t.Errorf("expected WriteFile of valid SKILL.md to succeed, got: %v", err)
	}

	// 3b. Invalid skill file write (missing instructions/steps) should fail
	invalidSkillMD := `---
name: test-skill
description: some desc
homepage: http://test.com
---

## Description
some desc

## Instructions
`
	err = WriteFile(wsDir, "my-skill/SKILL.md", invalidSkillMD)
	if err == nil {
		t.Error("expected WriteFile of invalid SKILL.md to fail, but it succeeded")
	}
}

func TestCreateSkillBlocksDuplicateName(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-dup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create initial skill
	err = CreateSkill(tmpDir, "code-review", "Review code for quality", "http://example.com", "- Check style\n- Check bugs")
	if err != nil {
		t.Fatalf("initial CreateSkill failed: %v", err)
	}

	// Attempt to create a skill with a very similar name
	err = CreateSkill(tmpDir, "code-reviews", "Different description entirely", "http://example.com", "- Do something else")
	if err == nil {
		t.Error("expected CreateSkill to block similar name, but it succeeded")
	}
}

func TestCreateSkillBlocksDuplicateDescription(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-dup-desc-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create initial skill
	err = CreateSkill(tmpDir, "git-helper", "Help with git commands and workflows", "http://example.com", "- Check git status\n- Suggest commits")
	if err != nil {
		t.Fatalf("initial CreateSkill failed: %v", err)
	}

	// Attempt to create a skill with a different name but very similar description
	err = CreateSkill(tmpDir, "version-control", "Help with git commands and workflows", "http://example.com", "- Do something")
	if err == nil {
		t.Error("expected CreateSkill to block similar description, but it succeeded")
	}
}

func TestCreateSkillSucceedsWhenNoSimilar(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-nodup-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create initial skill
	err = CreateSkill(tmpDir, "weather", "Get weather information", "http://weather.com", "- Fetch forecast")
	if err != nil {
		t.Fatalf("initial CreateSkill failed: %v", err)
	}

	// Create a completely different skill — should succeed
	err = CreateSkill(tmpDir, "code-formatter", "Format source code files", "http://format.com", "- Run prettier\n- Run eslint")
	if err != nil {
		t.Errorf("expected CreateSkill to succeed for unrelated skill, got: %v", err)
	}
}
