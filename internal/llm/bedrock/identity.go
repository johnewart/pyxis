package bedrockllm

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// CallerIdentityResult is what STS GetCallerIdentity reported for the credentials in use.
type CallerIdentityResult struct {
	Account string
	ARN     string
	UserID  string
}

// ResolveCallerIdentity builds credentials exactly as New does (default chain or
// profile, then AssumeRole when configured) and asks STS who they belong to.
// It makes no Bedrock call and persists nothing.
func ResolveCallerIdentity(ctx context.Context, cfg Config) (*CallerIdentityResult, error) {
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		return nil, fmt.Errorf("bedrock: region is required")
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if p := strings.TrimSpace(cfg.Profile); p != "" {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigProfile(p))
	}
	awscfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock: load AWS config: %w", err)
	}
	awscfg, err = applyAssumeRole(awscfg, cfg, sts.NewFromConfig(awscfg))
	if err != nil {
		return nil, err
	}
	return callerIdentity(ctx, sts.NewFromConfig(awscfg))
}

// STSIdentityAPI is the slice of the STS client used to read the caller identity.
type STSIdentityAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

func callerIdentity(ctx context.Context, api STSIdentityAPI) (*CallerIdentityResult, error) {
	out, err := api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("bedrock: sts GetCallerIdentity: %w", err)
	}
	return &CallerIdentityResult{
		Account: aws.ToString(out.Account),
		ARN:     aws.ToString(out.Arn),
		UserID:  aws.ToString(out.UserId),
	}, nil
}
