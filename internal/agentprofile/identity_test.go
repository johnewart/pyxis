package agentprofile

import (
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestBuild_IdentitySectionFirstWithNameAndDescription(t *testing.T) {
	p := sampleProfile("x")
	p.Name = "Ada"
	p.Description = "A careful research assistant."
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: "s1"}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	secs := req.GetSystemPromptSections()
	if len(secs) != 2 || secs[0].GetId() != IdentitySectionID || secs[1].GetId() != "persona" {
		t.Fatalf("want [%s persona], got %v", IdentitySectionID, secs)
	}
	c := secs[0].GetContent()
	if !strings.Contains(c, "Your name is Ada.") {
		t.Errorf("identity section lacks the name: %q", c)
	}
	if !strings.Contains(c, "Description of you: A careful research assistant.") {
		t.Errorf("identity section lacks the description: %q", c)
	}
}

func TestBuild_IdentitySectionOmitsBlankDescription(t *testing.T) {
	p := sampleProfile("x")
	p.Description = "  "
	p.SystemPromptSections = nil
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: "s1"}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	secs := req.GetSystemPromptSections()
	if len(secs) != 1 || secs[0].GetId() != IdentitySectionID {
		t.Fatalf("want only the identity section, got %v", secs)
	}
	if strings.Contains(secs[0].GetContent(), "Description of you") {
		t.Errorf("blank description should be left out: %q", secs[0].GetContent())
	}
}

func TestValidate_RejectsSectionWithContentButNoID(t *testing.T) {
	p := sampleProfile("x")
	p.SystemPromptSections = []*llmv1.SystemPromptSection{{Id: "  ", Content: "Be terse."}}
	err := Validate(&p)
	if err == nil || !strings.Contains(err.Error(), "has content but no id") {
		t.Fatalf("want a content-but-no-id error, got %v", err)
	}
}

func TestValidate_AcceptsEmptySection(t *testing.T) {
	p := sampleProfile("x")
	p.SystemPromptSections = []*llmv1.SystemPromptSection{{Id: "", Content: " "}}
	if err := Validate(&p); err != nil {
		t.Fatalf("an empty section carries nothing to lose: %v", err)
	}
}

func TestValidate_RejectsReservedIdentitySectionID(t *testing.T) {
	p := sampleProfile("x")
	p.SystemPromptSections = []*llmv1.SystemPromptSection{{Id: IdentitySectionID, Content: "I am someone else."}}
	err := Validate(&p)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("want a reserved-id error, got %v", err)
	}
}
