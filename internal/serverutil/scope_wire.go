package serverutil

// Scope wire names. This file is the ONLY place in the scope code that spells
// these names; everything else refers to the constants.
const (
	// scopeTenantMetadataKey is the gRPC metadata key carrying the tenant id.
	scopeTenantMetadataKey = "llmd-tenant-id"
	// scopeScopeMetadataKey is the gRPC metadata key carrying the scope id.
	scopeScopeMetadataKey = "llmd-scope-id"
	// scopeTenantHTTPHeader is the HTTP header carrying the tenant id.
	scopeTenantHTTPHeader = "X-Llmd-Tenant-Id"
	// scopeScopeHTTPHeader is the HTTP header carrying the scope id.
	scopeScopeHTTPHeader = "X-Llmd-Scope-Id"
	// scopeTenantQueryParam is the URL query parameter carrying the tenant id.
	scopeTenantQueryParam = "tenant_id"
	// scopeScopeQueryParam is the URL query parameter carrying the scope id.
	scopeScopeQueryParam = "scope_id"
	// scopePrincipalMetadataKey is the gRPC metadata key carrying the principal id.
	scopePrincipalMetadataKey = "llmd-principal-id"
	// scopePrincipalHTTPHeader is the HTTP header carrying the principal id.
	scopePrincipalHTTPHeader = "X-Llmd-Principal-Id"
	// scopePrincipalQueryParam is the URL query parameter carrying the principal id.
	scopePrincipalQueryParam = "principal_id"
)
