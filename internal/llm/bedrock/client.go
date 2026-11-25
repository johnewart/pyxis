package bedrockllm

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common"
)

const defaultMaxTokens int64 = 8192

// Config holds AWS and model defaults for Bedrock Converse.
type Config struct {
	Region        string
	Profile       string
	DefaultModel  string
	MaxOutputToks int64
	EndpointURL   string

	// AssumeRoleARN, when set, makes the client assume this role via STS.
	AssumeRoleARN string
	// RoleSessionName defaults to DefaultRoleSessionName when empty.
	RoleSessionName string
	// ExternalID is optional and must never be logged.
	ExternalID string
}

// DefaultRoleSessionName is used when Config.RoleSessionName is empty.
const DefaultRoleSessionName = "llmd-bedrock"

var (
	roleARNRe     = regexp.MustCompile(`^arn:aws(-[a-z]+)*:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$`)
	sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9+=,.@_-]{2,64}$`)
)

// ValidateAssumeRoleARN checks that arn is an IAM role ARN.
func ValidateAssumeRoleARN(arn string) error {
	if !roleARNRe.MatchString(arn) {
		return fmt.Errorf("bedrock: invalid assume role ARN %q", arn)
	}
	return nil
}

// ValidateRoleSessionName checks an STS role session name.
func ValidateRoleSessionName(name string) error {
	if !sessionNameRe.MatchString(name) {
		return fmt.Errorf("bedrock: invalid assume role session name %q (2-64 chars of A-Za-z0-9+=,.@_-)", name)
	}
	return nil
}

// applyAssumeRole returns base unchanged when no role is configured, else
// base with credentials replaced by a cached AssumeRoleProvider that calls
// STS through api.
func applyAssumeRole(base aws.Config, cfg Config, api stscreds.AssumeRoleAPIClient) (aws.Config, error) {
	arn := strings.TrimSpace(cfg.AssumeRoleARN)
	if arn == "" {
		return base, nil
	}
	if err := ValidateAssumeRoleARN(arn); err != nil {
		return base, err
	}
	session := strings.TrimSpace(cfg.RoleSessionName)
	if session == "" {
		session = DefaultRoleSessionName
	}
	if err := ValidateRoleSessionName(session); err != nil {
		return base, err
	}
	ext := strings.TrimSpace(cfg.ExternalID)
	prov := stscreds.NewAssumeRoleProvider(api, arn, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = session
		if ext != "" {
			o.ExternalID = aws.String(ext)
		}
	})
	base.Credentials = aws.NewCredentialsCache(prov)
	return base, nil
}

// Client implements backend.Backend via Bedrock Converse / ConverseStream.
type Client struct {
	rt       *bedrockruntime.Client
	ctrl     *bedrock.Client
	defModel string
	maxTok   int64
}

// New loads AWS configuration and builds runtime + control-plane clients.
func New(ctx context.Context, cfg Config) (*Client, error) {
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
	if strings.TrimSpace(cfg.AssumeRoleARN) != "" {
		awscfg, err = applyAssumeRole(awscfg, cfg, sts.NewFromConfig(awscfg))
		if err != nil {
			return nil, err
		}
	}
	ep := strings.TrimSpace(cfg.EndpointURL)
	rtOpt := func(o *bedrockruntime.Options) {
		if ep != "" {
			o.BaseEndpoint = aws.String(strings.TrimRight(ep, "/"))
		}
	}
	ctrlOpt := func(o *bedrock.Options) {
		if ep != "" {
			o.BaseEndpoint = aws.String(strings.TrimRight(ep, "/"))
		}
	}
	maxTok := cfg.MaxOutputToks
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	return &Client{
		rt:       bedrockruntime.NewFromConfig(awscfg, rtOpt),
		ctrl:     bedrock.NewFromConfig(awscfg, ctrlOpt),
		defModel: strings.TrimSpace(cfg.DefaultModel),
		maxTok:   maxTok,
	}, nil
}

// ControlClient exposes the Bedrock control plane (e.g. ListFoundationModels).
func (c *Client) ControlClient() *bedrock.Client {
	if c == nil {
		return nil
	}
	return c.ctrl
}

func (c *Client) inferenceConfig(opt *llmv1.CallOptions) *types.InferenceConfiguration {
	ic := &types.InferenceConfiguration{
		MaxTokens: aws.Int32(int32(c.maxTok)),
	}
	if opt == nil {
		return ic
	}
	if opt.GetMaxTokens() > 0 {
		ic.MaxTokens = aws.Int32(opt.GetMaxTokens())
	}
	if opt.Temperature != nil {
		ic.Temperature = aws.Float32(float32(*opt.Temperature))
	}
	if opt.TopP != nil {
		ic.TopP = aws.Float32(float32(*opt.TopP))
	}
	return ic
}

func (c *Client) buildConverseInput(messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*bedrockruntime.ConverseInput, error) {
	system, msgs, err := ProtoToBedrockMessages(messages, opt)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("bedrock: no messages to send after filtering")
	}
	model := strings.TrimSpace(common.PickModel(opt, c.defModel))
	if model == "" {
		return nil, fmt.Errorf("bedrock: model is required (set default_model on the provider or CallOptions.model)")
	}
	in := &bedrockruntime.ConverseInput{
		ModelId:         aws.String(model),
		Messages:        msgs,
		System:          system,
		InferenceConfig: c.inferenceConfig(opt),
	}
	if len(tools) > 0 {
		tc, err := toolConfigurationFromProto(tools)
		if err != nil {
			return nil, err
		}
		in.ToolConfig = tc
	}
	if err := applyToolChoice(in, opt, tools); err != nil {
		return nil, err
	}
	return in, nil
}

// Chat runs Converse (non-streaming).
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	in, err := c.buildConverseInput(messages, opt, tools)
	if err != nil {
		return nil, err
	}
	forced := applyResponseFormat(in, opt, len(tools))
	out, err := c.rt.Converse(ctx, in)
	if err != nil {
		return nil, err
	}
	msgOut, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok || msgOut.Value.Role != types.ConversationRoleAssistant {
		return nil, fmt.Errorf("bedrock: unexpected converse output type %T", out.Output)
	}
	reply, err := assistantReplyFromBedrockMessage(ctx, msgOut.Value, out.StopReason)
	if err != nil {
		return nil, err
	}
	if forced {
		common.LiftStructuredOutput(reply)
	}
	return reply, nil
}

// Generate maps system + user to Converse messages.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("bedrock: prompt must be non-empty")
	}
	var msgs []*llmv1.Message
	if t := strings.TrimSpace(systemPrompt); t != "" {
		msgs = append(msgs, &llmv1.Message{Role: "system", Content: t})
	}
	msgs = append(msgs, &llmv1.Message{Role: "user", Content: prompt})
	return c.Chat(ctx, msgs, opt, tools)
}
