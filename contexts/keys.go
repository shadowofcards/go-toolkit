package contexts

type contextKey string

const (
	KeyTenantID  contextKey = "tenantID"
	KeyUserID    contextKey = "userID"
	KeyUsername  contextKey = "username"
	KeyUserRoles contextKey = "userRoles"
	KeyPlayerID  contextKey = "playerID"
	KeyRequestID contextKey = "requestID"
	KeyOrigin    contextKey = "origin"
	KeyUserAgent contextKey = "userAgent"
)
