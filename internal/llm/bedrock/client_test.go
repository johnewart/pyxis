package bedrockllm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

type fakeSTS struct {
	in *sts.AssumeRoleInput
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.in = in
	return &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
		AccessKeyId:     aws.String("AK"),
		SecretAccessKey: aws.String("SK"),
		SessionToken:    aws.String("ST"),
		Expiration:      aws.Time(time.Now().Add(time.Hour)),
	}}, nil
}

const goodARN = "arn:aws:iam::123456789012:role/my-role"

func TestValidateAssumeRoleARN(t *testing.T) {
	good := []string{goodARN, "arn:aws-us-gov:iam::123456789012:role/path/r", "arn:aws-cn:iam::123456789012:role/r"}
	for _, a := range good {
		if err := ValidateAssumeRoleARN(a); err != nil {
			t.Errorf("%q: %v", a, err)
		}
	}
	bad := []string{"", "not-an-arn", "arn:awsfoo:iam::123456789012:role/r", "arn:aws:iam::123:role/r", "arn:aws:iam::123456789012:user/r"}
	for _, a := range bad {
		if err := ValidateAssumeRoleARN(a); err == nil || !strings.HasPrefix(err.Error(), "bedrock:") {
			t.Errorf("%q: expected bedrock error, got %v", a, err)
		}
	}
}

func TestValidateRoleSessionName(t *testing.T) {
	for _, n := range []string{"ab", DefaultRoleSessionName, "a+b=c,d.e@f_g-h"} {
		if err := ValidateRoleSessionName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "a", "bad name!", strings.Repeat("x", 65)} {
		if err := ValidateRoleSessionName(n); err == nil || !strings.HasPrefix(err.Error(), "bedrock:") {
			t.Errorf("%q: expected bedrock error, got %v", n, err)
		}
	}
}

func TestAssumeRoleBadARN(t *testing.T) {
	_, err := applyAssumeRole(aws.Config{}, Config{AssumeRoleARN: "not-an-arn"}, &fakeSTS{})
	if err == nil || !strings.HasPrefix(err.Error(), "bedrock:") {
		t.Fatalf("expected bedrock error, got %v", err)
	}
}

func TestAssumeRoleBadSessionName(t *testing.T) {
	_, err := applyAssumeRole(aws.Config{}, Config{AssumeRoleARN: goodARN, RoleSessionName: "bad name!"}, &fakeSTS{})
	if err == nil || !strings.HasPrefix(err.Error(), "bedrock:") {
		t.Fatalf("expected bedrock error, got %v", err)
	}
}

func TestAssumeRoleUnset(t *testing.T) {
	out, err := applyAssumeRole(aws.Config{}, Config{}, &fakeSTS{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Credentials != nil {
		t.Fatalf("credentials should be untouched")
	}
}

func TestAssumeRoleSetDefaultSession(t *testing.T) {
	f := &fakeSTS{}
	out, err := applyAssumeRole(aws.Config{}, Config{AssumeRoleARN: goodARN, ExternalID: "ext-1"}, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Credentials.(*aws.CredentialsCache); !ok {
		t.Fatalf("want *aws.CredentialsCache, got %T", out.Credentials)
	}
	if _, err := out.Credentials.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(f.in.RoleArn) != goodARN {
		t.Fatalf("role arn %q", aws.ToString(f.in.RoleArn))
	}
	if aws.ToString(f.in.RoleSessionName) != DefaultRoleSessionName {
		t.Fatalf("session %q", aws.ToString(f.in.RoleSessionName))
	}
	if aws.ToString(f.in.ExternalId) != "ext-1" {
		t.Fatalf("external id %q", aws.ToString(f.in.ExternalId))
	}
}
