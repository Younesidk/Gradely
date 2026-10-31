package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

// Gradly is its own OAuth 2.1 authorization server for the MCP endpoint: authorization code + PKCE (S256 only) for
// public clients, refresh-token rotation with reuse detection, RFC 7591 dynamic client registration, RFC 7009
// revocation, RFC 8414 / RFC 9728 metadata and RFC 8707 audience binding. Users sign in with their normal Gradly
// account; there is no second login system and no implicit or password grant.
const (
	authCodeLifetime     = 5 * time.Minute
	accessTokenLifetime  = time.Hour
	refreshTokenLifetime = 30 * 24 * time.Hour
	staleClientAge       = 30 * 24 * time.Hour
	maxRedirectURIs      = 5
)

var (
	pkcePattern         = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
	schemePattern       = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
	blockedSchemes      = []string{"javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss", "mailto", "tel", "sms"}
	supportedGrantTypes = []string{"authorization_code", "refresh_token"}
)

// ---------- metadata ----------

func (a *App) protectedResourceMetadata(c *gin.Context) {
	setOAuthCORS(c)
	c.JSON(http.StatusOK, oauthex.ProtectedResourceMetadata{
		Resource:               a.Cfg.MCPResource(),
		AuthorizationServers:   []string{a.Cfg.BaseURL},
		ScopesSupported:        allScopes(),
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Gradly",
		ResourceDocumentation:  a.Cfg.BaseURL + "/settings/integrations",
	})
}

func (a *App) authorizationServerMetadata(c *gin.Context) {
	setOAuthCORS(c)
	base := a.Cfg.BaseURL
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                         base,
		"authorization_endpoint":                         base + "/oauth/authorize",
		"token_endpoint":                                 base + "/oauth/token",
		"registration_endpoint":                          base + "/oauth/register",
		"revocation_endpoint":                            base + "/oauth/revoke",
		"scopes_supported":                               allScopes(),
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          supportedGrantTypes,
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          base + "/settings/integrations",
	})
}

// setOAuthCORS lets browser-based MCP clients reach the public OAuth endpoints. None of them use cookies, so a
// wildcard origin grants nothing a non-browser client couldn't already do.
func setOAuthCORS(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Protocol-Version")
}

func (a *App) oauthPreflight(c *gin.Context) {
	setOAuthCORS(c)
	c.Status(http.StatusNoContent)
}

// oauthError writes an RFC 6749 error response.
func oauthError(c *gin.Context, status int, code, description string) {
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, gin.H{"error": code, "error_description": description})
}

// ---------- redirect URIs ----------

func isLoopbackHost(host string) bool {
	h := strings.Trim(host, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// validRedirectURI accepts https URLs, http only on a loopback address (native apps, RFC 8252), and private-use
// schemes for desktop apps. Fragments, credentials and dangerous schemes are refused.
func validRedirectURI(s string) bool {
	if s == "" || len(s) > 500 || strings.ContainsAny(s, "#\\ \t\r\n") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Scheme == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
		return u.Host != ""
	case "http":
		return u.Host != "" && isLoopbackHost(u.Hostname())
	default:
		return schemePattern.MatchString(scheme) && !slices.Contains(blockedSchemes, scheme) && u.Opaque == ""
	}
}

// redirectMatches compares a requested redirect URI with a registered one exactly, except that a loopback URI may
// use any port (RFC 8252 §7.3), because native apps pick a free port at runtime.
func redirectMatches(registered, requested string) bool {
	if registered == requested {
		return true
	}
	r, err1 := url.Parse(registered)
	q, err2 := url.Parse(requested)
	if err1 != nil || err2 != nil || r.Scheme != "http" || q.Scheme != "http" {
		return false
	}
	return isLoopbackHost(r.Hostname()) && r.Hostname() == q.Hostname() && r.Path == q.Path && r.RawQuery == q.RawQuery
}

func clientAllows(cl *models.OAuthClient, requested string) bool {
	for _, u := range strings.Split(cl.RedirectURIs, "\n") {
		if redirectMatches(u, requested) {
			return true
		}
	}
	return false
}

// ---------- dynamic client registration (RFC 7591) ----------

func (a *App) registerClient(c *gin.Context) {
	setOAuthCORS(c)
	if !a.limiter.allow("register:"+c.ClientIP(), time.Now()) {
		c.Header("Retry-After", "60")
		oauthError(c, http.StatusTooManyRequests, "slow_down", "Too many registrations from this address. Try again in a minute.")
		return
	}
	var in struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		Scope                   string   `json:"scope"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&in); err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "The registration request must be a JSON object.")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > maxRedirectURIs {
		oauthError(c, http.StatusBadRequest, "invalid_redirect_uri", "Register between 1 and 5 redirect URIs.")
		return
	}
	for _, u := range in.RedirectURIs {
		if !validRedirectURI(u) {
			oauthError(c, http.StatusBadRequest, "invalid_redirect_uri",
				"Redirect URIs must use https, http on a loopback address, or a private app scheme, without a fragment.")
			return
		}
	}
	if m := in.TokenEndpointAuthMethod; m != "" && m != "none" {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "Only public clients (token_endpoint_auth_method \"none\") with PKCE are supported.")
		return
	}
	for _, g := range in.GrantTypes {
		if !slices.Contains(supportedGrantTypes, g) {
			oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "Supported grant types: authorization_code and refresh_token.")
			return
		}
	}
	for _, r := range in.ResponseTypes {
		if r != "code" {
			oauthError(c, http.StatusBadRequest, "invalid_client_metadata", "The only supported response type is \"code\".")
			return
		}
	}
	name := strings.TrimSpace(in.ClientName)
	if name == "" {
		name = "Unnamed application"
	}
	name = truncate(name, 100)

	now := a.now()
	cl := models.OAuthClient{ID: "gradly_client_" + newToken()[:32], Name: name, RedirectURIs: strings.Join(in.RedirectURIs, "\n"), CreatedAt: now}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// Registrations that never led to an authorization are dropped after a while, which bounds the table.
		tx.Where("created_at < ? AND id NOT IN (?)", now.Add(-staleClientAge), tx.Model(&models.OAuthGrant{}).Select("client_id")).
			Delete(&models.OAuthClient{})
		return tx.Create(&cl).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{
		"client_id": cl.ID, "client_id_issued_at": now.Unix(), "client_name": cl.Name,
		"redirect_uris": in.RedirectURIs, "grant_types": supportedGrantTypes, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// ---------- authorization (consent) ----------

type authorizeParams struct {
	ResponseType        string `json:"responseType" form:"response_type"`
	ClientID            string `json:"clientId" form:"client_id"`
	RedirectURI         string `json:"redirectUri" form:"redirect_uri"`
	Scope               string `json:"scope" form:"scope"`
	State               string `json:"state" form:"state"`
	CodeChallenge       string `json:"codeChallenge" form:"code_challenge"`
	CodeChallengeMethod string `json:"codeChallengeMethod" form:"code_challenge_method"`
	Resource            string `json:"resource" form:"resource"`
}

// authorizeRequest is a validated authorization request.
type authorizeRequest struct {
	client    *models.OAuthClient
	params    authorizeParams
	requested []string // known scopes the client asked for (all scopes when it asked for none)
	resource  string
}

// authorizeError is a failed authorization request. When redirect is empty the client or redirect URI could not be
// trusted, so the user is shown the error instead of being sent anywhere (RFC 6749 §4.1.2.1).
type authorizeError struct {
	code, description, redirect string
}

func (a *App) errorRedirect(p authorizeParams, code, description string) *authorizeError {
	q := url.Values{"error": {code}, "error_description": {description}, "iss": {a.Cfg.BaseURL}}
	if p.State != "" {
		q.Set("state", p.State)
	}
	return &authorizeError{code, description, appendQuery(p.RedirectURI, q)}
}

func appendQuery(uri string, q url.Values) string {
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + q.Encode()
}

func (a *App) validateAuthorize(p authorizeParams) (*authorizeRequest, *authorizeError) {
	if p.ClientID == "" || len(p.ClientID) > 64 {
		return nil, &authorizeError{code: "invalid_request", description: "The application didn't identify itself (missing client_id)."}
	}
	var cl models.OAuthClient
	if err := a.DB.First(&cl, "id = ?", p.ClientID).Error; err != nil {
		return nil, &authorizeError{code: "invalid_client", description: "This application isn't registered with Gradly. Ask it to connect again."}
	}
	if p.RedirectURI == "" || !clientAllows(&cl, p.RedirectURI) {
		return nil, &authorizeError{code: "invalid_request", description: "The application asked to return to an address it didn't register, so Gradly stopped here to keep your account safe."}
	}
	// From here on the redirect URI is trusted, so errors go back to the application.
	if p.ResponseType != "code" {
		return nil, a.errorRedirect(p, "unsupported_response_type", "Only the authorization code flow is supported.")
	}
	if p.CodeChallengeMethod != "S256" || !pkcePattern.MatchString(p.CodeChallenge) {
		return nil, a.errorRedirect(p, "invalid_request", "PKCE with code_challenge_method=S256 is required.")
	}
	if len(p.State) > 500 {
		return nil, a.errorRedirect(p, "invalid_request", "The state parameter is too long.")
	}
	resource := a.Cfg.MCPResource()
	if p.Resource != "" && strings.TrimRight(p.Resource, "/") != resource {
		return nil, a.errorRedirect(p, "invalid_target", "Tokens can only be issued for "+resource+".")
	}
	// Unknown scopes (such as "openid") are ignored rather than fatal; the user decides on the known ones.
	var requested []string
	for _, s := range strings.Fields(p.Scope) {
		if knownScope(s) {
			requested = append(requested, s)
		}
	}
	requested, _ = normalizeScopes(requested)
	if len(requested) == 0 {
		requested = allScopes()
	}
	return &authorizeRequest{client: &cl, params: p, requested: requested, resource: resource}, nil
}

// authorizeInfo validates the request for the consent screen and describes what is being asked.
func (a *App) authorizeInfo(c *gin.Context) {
	var p authorizeParams
	_ = c.ShouldBindQuery(&p)
	req, aerr := a.validateAuthorize(p)
	if aerr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": aerr.description, "code": aerr.code, "redirect": nilIfEmpty(aerr.redirect)})
		return
	}
	requested := make([]scopeInfo, 0, len(req.requested))
	for _, s := range scopeCatalog {
		if slices.Contains(req.requested, s.Scope) {
			requested = append(requested, s)
		}
	}
	var existing models.OAuthGrant
	var already []string
	if a.DB.Where("user_id = ? AND client_id = ? AND revoked_at IS NULL", current(c).ID, req.client.ID).First(&existing).Error == nil {
		already = splitScopes(existing.Scopes)
	}
	if already == nil {
		already = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "client": gin.H{"id": req.client.ID, "name": req.client.Name, "registeredAt": req.client.CreatedAt},
		"redirectHost": redirectHost(p.RedirectURI), "requested": requested, "scopeWasRequested": strings.TrimSpace(p.Scope) != "",
		"alreadyGranted": already, "resource": req.resource,
	})
}

// authorizeDecision records the signed-in user's answer. It re-validates everything: nothing from the consent page
// is trusted beyond the user's choice, and the granted scopes can only narrow what the application asked for.
func (a *App) authorizeDecision(c *gin.Context) {
	var in struct {
		authorizeParams
		Approve bool     `json:"approve"`
		Scopes  []string `json:"scopes"`
	}
	if !bind(c, &in) {
		return
	}
	req, aerr := a.validateAuthorize(in.authorizeParams)
	if aerr != nil {
		if aerr.redirect == "" {
			fail(c, badRequest(aerr.description))
			return
		}
		c.JSON(http.StatusOK, gin.H{"redirect": aerr.redirect})
		return
	}
	if !in.Approve {
		c.JSON(http.StatusOK, gin.H{"redirect": a.errorRedirect(in.authorizeParams, "access_denied", "The user declined the request.").redirect})
		return
	}
	chosen, unknown := normalizeScopes(in.Scopes)
	if unknown != "" {
		fail(c, badRequest("Unknown permission: "+unknown))
		return
	}
	granted := intersectScopes(chosen, req.requested)
	if len(granted) == 0 {
		fail(c, badRequest("Choose at least one permission, or deny the request."))
		return
	}
	code := newToken()
	now := a.now()
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		tx.Where("expires_at < ?", now).Delete(&models.OAuthCode{}) // tidy up
		return tx.Create(&models.OAuthCode{
			CodeHash: hashToken(code), UserID: user.ID, ClientID: req.client.ID, RedirectURI: in.RedirectURI,
			Scopes: joinScopes(granted), Resource: req.resource, CodeChallenge: in.CodeChallenge, ExpiresAt: now.Add(authCodeLifetime),
		}).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	q := url.Values{"code": {code}, "iss": {a.Cfg.BaseURL}}
	if in.State != "" {
		q.Set("state", in.State)
	}
	c.JSON(http.StatusOK, gin.H{"redirect": appendQuery(in.RedirectURI, q)})
}

// ---------- token endpoint ----------

func verifyPKCE(verifier, challenge string) bool {
	if !pkcePattern.MatchString(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func (a *App) token(c *gin.Context) {
	setOAuthCORS(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.Request.ParseForm(); err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request", "The token request must be form-encoded.")
		return
	}
	clientID := c.PostForm("client_id")
	if user, _, ok := c.Request.BasicAuth(); ok && clientID == "" {
		clientID, _ = url.QueryUnescape(user)
	}
	if clientID == "" {
		oauthError(c, http.StatusUnauthorized, "invalid_client", "client_id is required.")
		return
	}
	var cl models.OAuthClient
	if err := a.DB.First(&cl, "id = ?", clientID).Error; err != nil {
		oauthError(c, http.StatusUnauthorized, "invalid_client", "Unknown client.")
		return
	}
	switch c.PostForm("grant_type") {
	case "authorization_code":
		a.exchangeCode(c, &cl)
	case "refresh_token":
		a.refresh(c, &cl)
	default:
		oauthError(c, http.StatusBadRequest, "unsupported_grant_type", "Supported grant types: authorization_code, refresh_token.")
	}
}

type tokenPair struct {
	access, refresh string
	scopes          []string
}

// issueTokens stores a new access/refresh pair for the grant. It runs inside tx.
func (a *App) issueTokens(tx *gorm.DB, g *models.OAuthGrant, scopes []string, resource string) (*tokenPair, error) {
	now := a.now()
	tx.Where("expires_at < ?", now).Delete(&models.OAuthToken{}) // tidy up
	pair := &tokenPair{access: newSecret(accessTokenPrefix), refresh: newSecret(refreshTokenPrefix), scopes: scopes}
	rows := []models.OAuthToken{
		{TokenHash: hashToken(pair.access), Kind: models.TokenAccess, GrantID: g.ID, UserID: g.UserID, Scopes: joinScopes(scopes), Resource: resource, ExpiresAt: now.Add(accessTokenLifetime), CreatedAt: now},
		{TokenHash: hashToken(pair.refresh), Kind: models.TokenRefresh, GrantID: g.ID, UserID: g.UserID, Scopes: joinScopes(scopes), Resource: resource, ExpiresAt: now.Add(refreshTokenLifetime), CreatedAt: now},
	}
	if err := tx.Create(&rows).Error; err != nil {
		return nil, err
	}
	return pair, nil
}

func writeTokens(c *gin.Context, p *tokenPair) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, gin.H{
		"access_token": p.access, "token_type": "Bearer", "expires_in": int(accessTokenLifetime.Seconds()),
		"refresh_token": p.refresh, "scope": joinScopes(p.scopes),
	})
}

// oauthFailure carries an RFC 6749 error out of a transaction.
type oauthFailure struct {
	status            int
	code, description string
}

func (e *oauthFailure) Error() string { return e.description }

func invalidGrant(msg string) error {
	return &oauthFailure{http.StatusBadRequest, "invalid_grant", msg}
}

func (a *App) finishToken(c *gin.Context, pair *tokenPair, err error) {
	if err != nil {
		if of, ok := err.(*oauthFailure); ok {
			oauthError(c, of.status, of.code, of.description)
			return
		}
		fail(c, err)
		return
	}
	writeTokens(c, pair)
}

func (a *App) exchangeCode(c *gin.Context, cl *models.OAuthClient) {
	code, verifier, redirect := c.PostForm("code"), c.PostForm("code_verifier"), c.PostForm("redirect_uri")
	if code == "" || len(code) > 200 || verifier == "" {
		oauthError(c, http.StatusBadRequest, "invalid_request", "code and code_verifier are required.")
		return
	}
	now := a.now()
	var ac models.OAuthCode
	if err := a.DB.First(&ac, "code_hash = ?", hashToken(code)).Error; err != nil || ac.ClientID != cl.ID {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "The authorization code is invalid.")
		return
	}
	// Single use: only one exchange can flip used_at, and the claim is committed on its own so that any failed
	// attempt (wrong verifier, wrong redirect) burns the code too.
	res := a.DB.Model(&models.OAuthCode{}).Where("code_hash = ? AND used_at IS NULL", ac.CodeHash).Update("used_at", now)
	if res.Error != nil {
		fail(c, res.Error)
		return
	}
	if res.RowsAffected != 1 {
		if ac.GrantID != nil {
			// A replayed code means it leaked: revoke everything it produced (RFC 6749 §4.1.2).
			a.DB.Transaction(func(tx *gorm.DB) error { return a.revokeGrant(tx, *ac.GrantID) })
		}
		oauthError(c, http.StatusBadRequest, "invalid_grant", "The authorization code was already used.")
		return
	}
	var failure error
	switch {
	case !ac.ExpiresAt.After(now):
		failure = invalidGrant("The authorization code has expired.")
	case redirect != ac.RedirectURI:
		failure = invalidGrant("redirect_uri doesn't match the authorization request.")
	case !verifyPKCE(verifier, ac.CodeChallenge):
		failure = invalidGrant("PKCE verification failed.")
	case c.PostForm("resource") != "" && strings.TrimRight(c.PostForm("resource"), "/") != ac.Resource:
		failure = &oauthFailure{http.StatusBadRequest, "invalid_target", "The resource doesn't match the authorization request."}
	}
	if failure == nil {
		if _, err := a.activeUser(ac.UserID); err != nil {
			failure = invalidGrant("The account is not available.")
		}
	}
	if failure != nil {
		a.finishToken(c, nil, failure)
		return
	}

	var pair *tokenPair
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// One active grant per user and application; authorizing again updates its permissions.
		var g models.OAuthGrant
		err := tx.Where("user_id = ? AND client_id = ? AND revoked_at IS NULL", ac.UserID, cl.ID).First(&g).Error
		if err == nil {
			g.Scopes, g.Resource, g.UpdatedAt = ac.Scopes, ac.Resource, now
			if err := tx.Model(&g).Select("scopes", "resource", "updated_at").Updates(&g).Error; err != nil {
				return err
			}
		} else {
			g = models.OAuthGrant{UserID: ac.UserID, ClientID: cl.ID, Scopes: ac.Scopes, Resource: ac.Resource, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&g).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.OAuthCode{}).Where("code_hash = ?", ac.CodeHash).Update("grant_id", g.ID).Error; err != nil {
			return err
		}
		var err2 error
		pair, err2 = a.issueTokens(tx, &g, splitScopes(ac.Scopes), ac.Resource)
		return err2
	})
	a.finishToken(c, pair, err)
}

func (a *App) refresh(c *gin.Context, cl *models.OAuthClient) {
	presented := c.PostForm("refresh_token")
	if presented == "" || len(presented) > 200 || !strings.HasPrefix(presented, refreshTokenPrefix) {
		oauthError(c, http.StatusBadRequest, "invalid_grant", "The refresh token is invalid.")
		return
	}
	now := a.now()
	invalid := invalidGrant("The refresh token is invalid or was revoked.")
	var t models.OAuthToken
	if err := a.DB.First(&t, "token_hash = ? AND kind = ?", hashToken(presented), models.TokenRefresh).Error; err != nil {
		a.finishToken(c, nil, invalid)
		return
	}
	var g models.OAuthGrant
	if err := a.DB.First(&g, "id = ?", t.GrantID).Error; err != nil || g.ClientID != cl.ID || g.RevokedAt != nil {
		a.finishToken(c, nil, invalid)
		return
	}
	// Rotation: each refresh token works once. A second use means it was copied, so the whole grant goes.
	res := a.DB.Model(&models.OAuthToken{}).Where("token_hash = ? AND used_at IS NULL", t.TokenHash).Update("used_at", now)
	if res.Error != nil {
		fail(c, res.Error)
		return
	}
	if res.RowsAffected != 1 {
		a.DB.Transaction(func(tx *gorm.DB) error { return a.revokeGrant(tx, g.ID) })
		a.finishToken(c, nil, invalidGrant("The refresh token was already used. The connection was revoked for safety; authorize the application again."))
		return
	}
	if !t.ExpiresAt.After(now) {
		a.finishToken(c, nil, invalidGrant("The refresh token has expired. Authorize the application again."))
		return
	}
	if _, err := a.activeUser(g.UserID); err != nil {
		a.finishToken(c, nil, invalidGrant("The account is not available."))
		return
	}
	scopes := splitScopes(t.Scopes)
	if s := strings.TrimSpace(c.PostForm("scope")); s != "" {
		asked := strings.Fields(s)
		for _, x := range asked {
			if !slices.Contains(scopes, x) {
				oauthError(c, http.StatusBadRequest, "invalid_scope", "A refresh can't add permissions that weren't granted.")
				return
			}
		}
		scopes, _ = normalizeScopes(asked)
	}
	var pair *tokenPair
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// The grant may have been revoked since it was read; re-check inside the transaction (revocation race).
		res := tx.Model(&models.OAuthGrant{}).Where("id = ? AND revoked_at IS NULL", g.ID).Update("updated_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return invalid
		}
		var err error
		pair, err = a.issueTokens(tx, &g, scopes, t.Resource)
		return err
	})
	a.finishToken(c, pair, err)
}

// revokeToken implements RFC 7009. Revoking a refresh token ends the whole connection; revoking an access token
// ends just that token. The answer is always 200 so it can't be used to probe for valid tokens.
func (a *App) revokeToken(c *gin.Context) {
	setOAuthCORS(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	_ = c.Request.ParseForm()
	token, clientID := c.PostForm("token"), c.PostForm("client_id")
	if user, _, ok := c.Request.BasicAuth(); ok && clientID == "" {
		clientID, _ = url.QueryUnescape(user)
	}
	if token != "" && len(token) <= 200 {
		var t models.OAuthToken
		if a.DB.First(&t, "token_hash = ?", hashToken(token)).Error == nil {
			var g models.OAuthGrant
			if a.DB.First(&g, "id = ?", t.GrantID).Error == nil && g.ClientID == clientID {
				if t.Kind == models.TokenRefresh {
					a.DB.Transaction(func(tx *gorm.DB) error { return a.revokeGrant(tx, g.ID) })
				} else {
					a.DB.Delete(&t)
				}
			}
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
}
