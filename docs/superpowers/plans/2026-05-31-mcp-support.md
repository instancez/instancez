# MCP Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose Ultrabase Cloud project management and raw SQL data access as a remote MCP server, authenticated via OAuth 2.0 authorization code + PKCE flow.

**Architecture:** Two services in `instancez-coder/v2` are modified — `auth` gains OAuth 2.0 endpoints that issue PATs, and `data` gains a `ProjectService` layer (extracted from existing handlers) plus a `POST /mcp` JSON-RPC endpoint. No new deployment needed.

**Tech Stack:** Go 1.24, Gin, MongoDB (via `utils.MongoClient`), pgx v5 (already in `data`), MCP 2025 Streamable HTTP transport (JSON-RPC over HTTP, no external SDK needed).

---

## File Map

### `instancez-coder/v2/utils/`
- **Modify** `types.go` — add `OAuthClient`, `OAuthCode` structs
- **Modify** `const.go` — add `COLLECTION_OAUTH_CLIENTS`, `COLLECTION_OAUTH_CODES`

### `instancez-coder/v2/auth/pkg/server/`
- **Create** `oauth_handlers.go` — `WellKnownOAuthHandler`, `OAuthRegisterHandler`, `OAuthAuthorizeHandler`, `OAuthTokenHandler`
- **Create** `oauth_handlers_test.go`
- **Modify** `handler.go` — register OAuth routes + `EnsureOAuthIndexes`

### `instancez-coder/v2/data/pkg/server/`
- **Create** `project_service.go` — `ProjectService` struct with all business logic
- **Create** `project_service_test.go`
- **Modify** `ultrabase_handlers.go` — replace inline logic with `ProjectService` calls; add `ListProjects`, `GetProject`, `DeleteProject` handlers
- **Modify** `handler.go` — register new REST routes + MCP routes
- **Create** `mcp_handler.go` — `MCPHandler`: `initialize`, `tools/list`, `tools/call` dispatch
- **Create** `mcp_handler_test.go`

---

## Task 1: Add shared types and collection constants

**Files:**
- Modify: `instancez-coder/v2/utils/types.go`
- Modify: `instancez-coder/v2/utils/const.go`

- [ ] **Step 1: Add OAuthClient and OAuthCode structs to types.go**

  Append to `utils/types.go` before the closing of the file (after `DeviceFlow`):

  ```go
  // OAuthClient represents a registered MCP/OAuth client (dynamic registration, RFC 7591).
  type OAuthClient struct {
  	Id           string    `json:"_id,omitempty" bson:"_id,omitempty"`
  	ClientID     string    `json:"client_id" bson:"client_id"`
  	Name         string    `json:"name,omitempty" bson:"name,omitempty"`
  	RedirectURIs []string  `json:"redirect_uris" bson:"redirect_uris"`
  	CreatedAt    time.Time `json:"created_at" bson:"created_at"`
  }

  // OAuthCode is a short-lived, single-use authorization code issued by
  // GET /oauth/authorize. Exchanged for a PAT via POST /oauth/token.
  type OAuthCode struct {
  	Id            string    `json:"_id,omitempty" bson:"_id,omitempty"`
  	Code          string    `json:"code" bson:"code"`
  	ClientID      string    `json:"client_id" bson:"client_id"`
  	UserID        string    `json:"user_id" bson:"user_id"`
  	RedirectURI   string    `json:"redirect_uri" bson:"redirect_uri"`
  	CodeChallenge string    `json:"code_challenge" bson:"code_challenge"`
  	ExpiresAt     time.Time `json:"expires_at" bson:"expires_at"`
  	CreatedAt     time.Time `json:"created_at" bson:"created_at"`
  }
  ```

- [ ] **Step 2: Add collection constants to const.go**

  Append to the `const` block in `utils/const.go` (after `COLLECTION_USER_PATS`):

  ```go
  // OAuth 2.0 dynamic client registration and authorization codes
  COLLECTION_OAUTH_CLIENTS Collection = "oauth_clients"
  COLLECTION_OAUTH_CODES   Collection = "oauth_codes"
  ```

- [ ] **Step 3: Build utils to verify no compile errors**

  ```bash
  cd instancez-coder/v2/utils && go build ./...
  ```
  Expected: no output (success).

- [ ] **Step 4: Commit**

  ```bash
  git add utils/types.go utils/const.go
  git commit -m "feat(utils): add OAuthClient, OAuthCode types and collection constants"
  ```

---

## Task 2: OAuth discovery and dynamic client registration

**Files:**
- Create: `instancez-coder/v2/auth/pkg/server/oauth_handlers.go`
- Create: `instancez-coder/v2/auth/pkg/server/oauth_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

  Create `auth/pkg/server/oauth_handlers_test.go`:

  ```go
  package server

  import (
  	"encoding/json"
  	"net/http"
  	"net/http/httptest"
  	"strings"
  	"testing"

  	"github.com/gin-gonic/gin"
  	"github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  )

  func newTestAuthHandler() *AuthHandler {
  	return &AuthHandler{
  		MongoDB:      nil, // stubbed per test
  		DashboardURL: "https://app.ultrabase.com",
  		JWTSecret:    []byte("test-secret"),
  	}
  }

  func TestWellKnownOAuthHandler(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := newTestAuthHandler()
  	r := gin.New()
  	r.GET("/.well-known/oauth-authorization-server", h.WellKnownOAuthHandler)

  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
  	r.ServeHTTP(w, req)

  	require.Equal(t, http.StatusOK, w.Code)
  	var body map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
  	assert.Equal(t, "https://app.ultrabase.com/oauth/authorize", body["authorization_endpoint"])
  	assert.Equal(t, "https://app.ultrabase.com/oauth/token", body["token_endpoint"])
  	assert.Equal(t, "https://app.ultrabase.com/oauth/register", body["registration_endpoint"])
  }

  func TestOAuthRegisterHandlerRejectsEmptyRedirectURIs(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := newTestAuthHandler()
  	r := gin.New()
  	r.POST("/oauth/register", h.OAuthRegisterHandler)

  	body := `{"client_name": "test"}`
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	assert.Equal(t, http.StatusBadRequest, w.Code)
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  ```bash
  cd instancez-coder/v2/auth && go test ./pkg/server/... -run "TestWellKnown|TestOAuthRegister" -v
  ```
  Expected: FAIL — `WellKnownOAuthHandler` and `OAuthRegisterHandler` undefined.

- [ ] **Step 3: Implement WellKnownOAuthHandler and OAuthRegisterHandler**

  Create `auth/pkg/server/oauth_handlers.go`:

  ```go
  package server

  import (
  	"crypto/rand"
  	"encoding/hex"
  	"log"
  	"net/http"
  	"time"

  	"github.com/gin-gonic/gin"
  	"github.com/instancez/utils"
  )

  // generateHex returns n random bytes as a hex string.
  func generateHex(n int) string {
  	b := make([]byte, n)
  	if _, err := rand.Read(b); err != nil {
  		panic("crypto/rand failed: " + err.Error())
  	}
  	return hex.EncodeToString(b)
  }

  // WellKnownOAuthHandler returns OAuth 2.0 authorization server metadata (RFC 8414).
  // Required by MCP clients for endpoint discovery.
  func (h *AuthHandler) WellKnownOAuthHandler(c *gin.Context) {
  	base := h.DashboardURL
  	c.JSON(http.StatusOK, gin.H{
  		"issuer":                                 base,
  		"authorization_endpoint":                base + "/oauth/authorize",
  		"token_endpoint":                        base + "/oauth/token",
  		"registration_endpoint":                 base + "/oauth/register",
  		"response_types_supported":              []string{"code"},
  		"grant_types_supported":                 []string{"authorization_code"},
  		"code_challenge_methods_supported":      []string{"S256"},
  		"token_endpoint_auth_methods_supported": []string{"none"},
  	})
  }

  type oauthRegisterRequest struct {
  	ClientName   string   `json:"client_name"`
  	RedirectURIs []string `json:"redirect_uris" binding:"required,min=1"`
  }

  // OAuthRegisterHandler handles dynamic client registration (RFC 7591).
  // Stores minimal client metadata and returns a client_id.
  func (h *AuthHandler) OAuthRegisterHandler(c *gin.Context) {
  	var req oauthRegisterRequest
  	if err := c.ShouldBindJSON(&req); err != nil {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "redirect_uris is required"})
  		return
  	}

  	clientID := "mcp_" + generateHex(16)
  	client := utils.OAuthClient{
  		ClientID:     clientID,
  		Name:         req.ClientName,
  		RedirectURIs: req.RedirectURIs,
  		CreatedAt:    time.Now(),
  	}
  	if err := h.MongoDB.Create(c, utils.COLLECTION_OAUTH_CLIENTS, client); err != nil {
  		log.Printf("oauth register: %v", err)
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register client"})
  		return
  	}
  	c.JSON(http.StatusCreated, gin.H{
  		"client_id":     clientID,
  		"redirect_uris": req.RedirectURIs,
  	})
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  ```bash
  cd instancez-coder/v2/auth && go test ./pkg/server/... -run "TestWellKnown|TestOAuthRegister" -v
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add auth/pkg/server/oauth_handlers.go auth/pkg/server/oauth_handlers_test.go
  git commit -m "feat(auth): OAuth discovery and dynamic client registration"
  ```

---

## Task 3: OAuth authorize endpoint

**Files:**
- Modify: `instancez-coder/v2/auth/pkg/server/oauth_handlers.go`
- Modify: `instancez-coder/v2/auth/pkg/server/oauth_handlers_test.go`

- [ ] **Step 1: Add authorize tests**

  Append to `auth/pkg/server/oauth_handlers_test.go`:

  ```go
  func TestOAuthAuthorizeRedirectsWithoutSession(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := newTestAuthHandler()
  	r := gin.New()
  	r.GET("/oauth/authorize", h.OAuthAuthorizeHandler)

  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodGet,
  		"/oauth/authorize?client_id=mcp_abc&response_type=code&redirect_uri=http://localhost:1234/cb&code_challenge=abc123&code_challenge_method=S256",
  		nil)
  	r.ServeHTTP(w, req)

  	// No session cookie → redirect to login
  	assert.Equal(t, http.StatusFound, w.Code)
  	assert.Contains(t, w.Header().Get("Location"), "/login?next=")
  }

  func TestOAuthAuthorizeRejectsMissingParams(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := newTestAuthHandler()
  	r := gin.New()
  	r.GET("/oauth/authorize", h.OAuthAuthorizeHandler)

  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code", nil)
  	r.ServeHTTP(w, req)

  	assert.Equal(t, http.StatusBadRequest, w.Code)
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  ```bash
  cd instancez-coder/v2/auth && go test ./pkg/server/... -run "TestOAuthAuthorize" -v
  ```
  Expected: FAIL — `OAuthAuthorizeHandler` undefined.

- [ ] **Step 3: Implement OAuthAuthorizeHandler**

  Append to `auth/pkg/server/oauth_handlers.go`:

  ```go
  import (
  	// add to existing imports:
  	"net/url"
  	"slices"

  	"github.com/golang-jwt/jwt/v5"
  	"go.mongodb.org/mongo-driver/v2/bson"
  )

  // getUserIDFromCookie validates the "token" JWT cookie and returns the user_id (email),
  // or "" if the cookie is missing or invalid.
  func (h *AuthHandler) getUserIDFromCookie(c *gin.Context) string {
  	tokenStr, err := c.Cookie("token")
  	if err != nil {
  		return ""
  	}
  	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
  		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
  			return nil, fmt.Errorf("unexpected alg")
  		}
  		return h.JWTSecret, nil
  	})
  	if err != nil || !token.Valid {
  		return ""
  	}
  	claims, ok := token.Claims.(jwt.MapClaims)
  	if !ok {
  		return ""
  	}
  	sub, _ := claims["sub"].(string)
  	return sub
  }

  // OAuthAuthorizeHandler handles GET /oauth/authorize (authorization code flow, RFC 6749 §4.1).
  // Unauthenticated requests are redirected to the login page.
  // Authenticated requests receive a one-time authorization code in the redirect.
  func (h *AuthHandler) OAuthAuthorizeHandler(c *gin.Context) {
  	clientID := c.Query("client_id")
  	responseType := c.Query("response_type")
  	redirectURI := c.Query("redirect_uri")
  	codeChallenge := c.Query("code_challenge")
  	challengeMethod := c.Query("code_challenge_method")
  	state := c.Query("state")

  	if responseType != "code" || clientID == "" || redirectURI == "" || codeChallenge == "" {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
  		return
  	}
  	if challengeMethod != "S256" {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_challenge_method"})
  		return
  	}

  	// Verify the client and redirect_uri are registered.
  	var client utils.OAuthClient
  	if err := h.MongoDB.ReadOne(c, utils.COLLECTION_OAUTH_CLIENTS, &client, bson.M{"client_id": clientID}); err != nil {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client"})
  		return
  	}
  	if !slices.Contains(client.RedirectURIs, redirectURI) {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_redirect_uri"})
  		return
  	}

  	// Require a valid session.
  	userID := h.getUserIDFromCookie(c)
  	if userID == "" {
  		loginURL := h.DashboardURL + "/login?next=" + url.QueryEscape(c.Request.URL.RequestURI())
  		c.Redirect(http.StatusFound, loginURL)
  		return
  	}

  	// Issue authorization code.
  	code := "oac_" + generateHex(24)
  	now := time.Now()
  	oauthCode := utils.OAuthCode{
  		Code:          code,
  		ClientID:      clientID,
  		UserID:        userID,
  		RedirectURI:   redirectURI,
  		CodeChallenge: codeChallenge,
  		ExpiresAt:     now.Add(5 * time.Minute),
  		CreatedAt:     now,
  	}
  	if err := h.MongoDB.Create(c, utils.COLLECTION_OAUTH_CODES, oauthCode); err != nil {
  		log.Printf("oauth authorize: store code: %v", err)
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
  		return
  	}

  	callbackURL := redirectURI + "?code=" + url.QueryEscape(code)
  	if state != "" {
  		callbackURL += "&state=" + url.QueryEscape(state)
  	}
  	c.Redirect(http.StatusFound, callbackURL)
  }
  ```

  > **Note:** Move the `import` additions into the existing import block at the top of `oauth_handlers.go` — Go imports must be at the top, not inline. The block above shows which packages to add.

- [ ] **Step 4: Run tests to verify they pass**

  ```bash
  cd instancez-coder/v2/auth && go test ./pkg/server/... -run "TestOAuthAuthorize" -v
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add auth/pkg/server/oauth_handlers.go auth/pkg/server/oauth_handlers_test.go
  git commit -m "feat(auth): OAuth authorize endpoint with PKCE and session check"
  ```

---

## Task 4: OAuth token endpoint

**Files:**
- Modify: `instancez-coder/v2/auth/pkg/server/oauth_handlers.go`
- Modify: `instancez-coder/v2/auth/pkg/server/oauth_handlers_test.go`
- Modify: `instancez-coder/v2/auth/pkg/server/handler.go`

- [ ] **Step 1: Add token endpoint tests**

  Append to `auth/pkg/server/oauth_handlers_test.go`:

  ```go
  func TestOAuthTokenHandlerRejectsWrongGrantType(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := newTestAuthHandler()
  	r := gin.New()
  	r.POST("/oauth/token", h.OAuthTokenHandler)

  	body := `{"grant_type":"client_credentials","code":"x","code_verifier":"y","client_id":"z","redirect_uri":"http://localhost/cb"}`
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	assert.Equal(t, http.StatusBadRequest, w.Code)
  	var resp map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
  	assert.Equal(t, "unsupported_grant_type", resp["error"])
  }

  func TestOAuthTokenHandlerRejectsMissingFields(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := newTestAuthHandler()
  	r := gin.New()
  	r.POST("/oauth/token", h.OAuthTokenHandler)

  	body := `{"grant_type":"authorization_code"}`
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	assert.Equal(t, http.StatusBadRequest, w.Code)
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  ```bash
  cd instancez-coder/v2/auth && go test ./pkg/server/... -run "TestOAuthToken" -v
  ```
  Expected: FAIL — `OAuthTokenHandler` undefined.

- [ ] **Step 3: Implement OAuthTokenHandler**

  Append to `auth/pkg/server/oauth_handlers.go` (add `crypto/sha256`, `crypto/subtle`, `encoding/base64`, `fmt` to imports):

  ```go
  type oauthTokenRequest struct {
  	GrantType    string `json:"grant_type" binding:"required"`
  	Code         string `json:"code" binding:"required"`
  	CodeVerifier string `json:"code_verifier" binding:"required"`
  	ClientID     string `json:"client_id" binding:"required"`
  	RedirectURI  string `json:"redirect_uri" binding:"required"`
  }

  // OAuthTokenHandler handles POST /oauth/token.
  // Verifies the authorization code + PKCE verifier, then mints a PAT.
  func (h *AuthHandler) OAuthTokenHandler(c *gin.Context) {
  	var req oauthTokenRequest
  	if err := c.ShouldBindJSON(&req); err != nil {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
  		return
  	}
  	if req.GrantType != "authorization_code" {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
  		return
  	}

  	var oauthCode utils.OAuthCode
  	filter := bson.M{"code": req.Code, "client_id": req.ClientID}
  	if err := h.MongoDB.ReadOne(c, utils.COLLECTION_OAUTH_CODES, &oauthCode, filter); err != nil {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
  		return
  	}
  	if time.Now().After(oauthCode.ExpiresAt) {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
  		return
  	}
  	if oauthCode.RedirectURI != req.RedirectURI {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
  		return
  	}

  	// Verify PKCE: base64url(SHA-256(code_verifier)) must equal code_challenge.
  	h256 := sha256.Sum256([]byte(req.CodeVerifier))
  	computed := base64.RawURLEncoding.EncodeToString(h256[:])
  	if subtle.ConstantTimeCompare([]byte(computed), []byte(oauthCode.CodeChallenge)) != 1 {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
  		return
  	}

  	// Delete the code so it cannot be reused.
  	_ = h.MongoDB.Delete(c, utils.COLLECTION_OAUTH_CODES, bson.M{"code": req.Code})

  	// Mint a PAT.
  	raw, err := generatePAT()
  	if err != nil {
  		log.Printf("oauth token: generate PAT: %v", err)
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
  		return
  	}
  	pat := utils.PersonalAccessToken{
  		UserID:    oauthCode.UserID,
  		KeyHash:   hashPAT(raw),
  		KeyPrefix: patPrefix(raw),
  		Name:      "MCP (OAuth)",
  		CreatedAt: time.Now(),
  	}
  	if _, err := h.MongoDB.InsertOne(c, utils.COLLECTION_USER_PATS, pat); err != nil {
  		log.Printf("oauth token: insert PAT: %v", err)
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
  		return
  	}

  	c.JSON(http.StatusOK, gin.H{
  		"access_token": raw,
  		"token_type":   "Bearer",
  	})
  }
  ```

- [ ] **Step 4: Register OAuth routes in handler.go**

  In `auth/pkg/server/handler.go`, add the following routes to `NewRouter` (place them before the `return r` at the end, or after the device flow routes):

  ```go
  // OAuth 2.0 for MCP clients. No auth required on discovery/register/token.
  r.GET("/.well-known/oauth-authorization-server", h.WellKnownOAuthHandler)
  r.POST("/oauth/register", h.OAuthRegisterHandler)
  r.GET("/oauth/authorize", h.OAuthAuthorizeHandler)
  r.POST("/oauth/token", h.OAuthTokenHandler)
  ```

- [ ] **Step 5: Add EnsureOAuthIndexes and call it at startup**

  Append to `auth/pkg/server/oauth_handlers.go`:

  ```go
  // EnsureOAuthIndexes creates TTL and unique indexes for OAuth collections.
  // Call once at startup after EnsureDeviceFlowIndexes.
  func (h *AuthHandler) EnsureOAuthIndexes() {
  	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  	defer cancel()

  	db := h.MongoDB.Database(h.MongoDB.DBName)

  	// oauth_codes: TTL on expires_at, unique on code
  	coll := db.Collection(string(utils.COLLECTION_OAUTH_CODES))
  	_, err := coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
  		{
  			Keys:    bson.D{{Key: "expires_at", Value: 1}},
  			Options: options.Index().SetExpireAfterSeconds(0),
  		},
  		{
  			Keys:    bson.D{{Key: "code", Value: 1}},
  			Options: options.Index().SetUnique(true),
  		},
  	})
  	if err != nil {
  		log.Printf("warning: oauth_codes indexes: %v", err)
  	}

  	// oauth_clients: unique on client_id
  	clientColl := db.Collection(string(utils.COLLECTION_OAUTH_CLIENTS))
  	_, err = clientColl.Indexes().CreateOne(ctx, mongo.IndexModel{
  		Keys:    bson.D{{Key: "client_id", Value: 1}},
  		Options: options.Index().SetUnique(true),
  	})
  	if err != nil {
  		log.Printf("warning: oauth_clients indexes: %v", err)
  	}
  }
  ```

  Add `"go.mongodb.org/mongo-driver/v2/mongo"` and `"go.mongodb.org/mongo-driver/v2/mongo/options"` to imports in `oauth_handlers.go`.

  Find where `h.EnsureDeviceFlowIndexes()` is called in the auth `main.go` or `cmd/` and call `h.EnsureOAuthIndexes()` right after it.

- [ ] **Step 6: Run all auth tests**

  ```bash
  cd instancez-coder/v2/auth && go test ./... -v
  ```
  Expected: all PASS, no compile errors.

- [ ] **Step 7: Commit**

  ```bash
  git add auth/pkg/server/oauth_handlers.go auth/pkg/server/oauth_handlers_test.go auth/pkg/server/handler.go
  git commit -m "feat(auth): OAuth token endpoint and route registration"
  ```

---

## Task 5: Extract ProjectService from ultrabase_handlers.go

**Files:**
- Create: `instancez-coder/v2/data/pkg/server/project_service.go`
- Create: `instancez-coder/v2/data/pkg/server/project_service_test.go`
- Modify: `instancez-coder/v2/data/pkg/server/ultrabase_handlers.go`

- [ ] **Step 1: Write failing tests for ProjectService**

  Create `data/pkg/server/project_service_test.go`:

  ```go
  package server

  import (
  	"context"
  	"testing"

  	"github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  )

  // stubMongo is a minimal fake MongoClient for unit tests that don't hit a real DB.
  // For integration tests use the real MongoClient against a testcontainer.
  // Tests that need real DB behaviour should be in *_integration_test.go files.

  func TestProjectServiceWhoami(t *testing.T) {
  	svc := &ProjectService{handler: &DataHandler{}}
  	result, err := svc.Whoami(context.Background(), "user@example.com")
  	require.NoError(t, err)
  	assert.Equal(t, "user@example.com", result.Email)
  	assert.Equal(t, "user@example.com", result.UserID)
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestProjectService" -v
  ```
  Expected: FAIL — `ProjectService` undefined.

- [ ] **Step 3: Create project_service.go with ProjectService struct and Whoami**

  Create `data/pkg/server/project_service.go`:

  ```go
  package server

  import (
  	"context"
  	"fmt"
  	"log"
  	"time"

  	"github.com/instancez/utils"
  	"go.mongodb.org/mongo-driver/v2/bson"
  	"go.mongodb.org/mongo-driver/v2/mongo"
  )

  // ProjectService contains business logic for ultrabase project operations.
  // Both the REST handlers and MCP tools call into this; neither duplicates logic.
  type ProjectService struct {
  	handler *DataHandler
  }

  // WhoamiResult is returned by Whoami.
  type WhoamiResult struct {
  	Email  string `json:"email"`
  	UserID string `json:"user_id"`
  }

  // Whoami returns the identity for the given userID (email == user_id in this system).
  func (s *ProjectService) Whoami(_ context.Context, userID string) (*WhoamiResult, error) {
  	return &WhoamiResult{Email: userID, UserID: userID}, nil
  }

  // ProjectSummary is a lightweight project representation for list responses.
  type ProjectSummary struct {
  	ID        string    `json:"id"`
  	Name      string    `json:"name"`
  	Slug      string    `json:"slug"`
  	Domain    string    `json:"domain"`
  	Status    string    `json:"status"`
  	CreatedAt time.Time `json:"created_at"`
  }

  // CreateResult is returned by CreateProject.
  type CreateResult struct {
  	ProjectID string `json:"project_id"`
  	Slug      string `json:"slug"`
  	Name      string `json:"name"`
  }

  // DeployResult is returned by DeployProject.
  type DeployResult struct {
  	VersionID string `json:"version_id"`
  	Message   string `json:"message,omitempty"`
  }

  // MigrationPreviewResult is returned by MigrationPreview.
  type MigrationPreviewResult struct {
  	Diff string `json:"diff"`
  }

  // CreateProject creates a new backend-only App with a unique slug.
  func (s *ProjectService) CreateProject(ctx context.Context, userID, name string) (*CreateResult, error) {
  	h := s.handler
  	now := time.Now()
  	appID := bson.NewObjectID().Hex()

  	const maxRetries = 10
  	var app utils.App
  	var err error
  	for i := 0; i < maxRetries; i++ {
  		slug := utils.GenerateSlug()
  		app = utils.App{
  			Id:            appID,
  			Slug:          slug,
  			Owner:         userID,
  			GenericDomain: fmt.Sprintf("%s.%s", slug, h.BaseDomain),
  			Name:          name,
  			Type:          utils.AppTypeBackend,
  			Status:        "active",
  			CreatedAt:     now,
  			UpdatedAt:     now,
  		}
  		err = h.MongoDB.Create(ctx, utils.COLLECTION_APPS, app)
  		if err != nil {
  			if mongo.IsDuplicateKeyError(err) {
  				appID = bson.NewObjectID().Hex()
  				continue
  			}
  			return nil, fmt.Errorf("create app: %w", err)
  		}
  		break
  	}
  	if err != nil {
  		return nil, fmt.Errorf("failed to generate unique slug after %d attempts", maxRetries)
  	}
  	return &CreateResult{ProjectID: app.Id, Slug: app.Slug, Name: app.Name}, nil
  }

  // GetProject returns a single project by ID, verifying ownership.
  func (s *ProjectService) GetProject(ctx context.Context, userID, projectID string) (*ProjectSummary, error) {
  	var app utils.App
  	filter := bson.M{
  		"_id":   projectID,
  		"Owner": userID,
  		"$or": []bson.M{
  			{"Deleted": false},
  			{"Deleted": bson.M{"$exists": false}},
  		},
  	}
  	if err := s.handler.MongoDB.ReadOne(ctx, utils.COLLECTION_APPS, &app, filter); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return nil, errNotFound("project not found")
  		}
  		return nil, fmt.Errorf("get project: %w", err)
  	}
  	return appToSummary(&app), nil
  }

  // ListProjects returns all non-deleted projects owned by userID.
  func (s *ProjectService) ListProjects(ctx context.Context, userID string) ([]*ProjectSummary, error) {
  	var apps []utils.App
  	filter := bson.M{
  		"Owner": userID,
  		"Type":  utils.AppTypeBackend,
  		"$or": []bson.M{
  			{"Deleted": false},
  			{"Deleted": bson.M{"$exists": false}},
  		},
  	}
  	if err := s.handler.MongoDB.Read(ctx, utils.COLLECTION_APPS, &apps, filter); err != nil {
  		return nil, fmt.Errorf("list projects: %w", err)
  	}
  	out := make([]*ProjectSummary, len(apps))
  	for i := range apps {
  		out[i] = appToSummary(&apps[i])
  	}
  	return out, nil
  }

  // DeleteProject soft-deletes a project. Returns errNotFound if not owned by userID.
  func (s *ProjectService) DeleteProject(ctx context.Context, userID, projectID string) error {
  	now := time.Now()
  	filter := bson.M{"_id": projectID, "Owner": userID}
  	update := bson.M{"$set": bson.M{"Deleted": true, "DeletedAt": now, "UpdatedAt": now}}
  	result, err := s.handler.MongoDB.UpdateOne(ctx, utils.COLLECTION_APPS, filter, update)
  	if err != nil {
  		return fmt.Errorf("delete project: %w", err)
  	}
  	if result.MatchedCount == 0 {
  		return errNotFound("project not found")
  	}
  	return nil
  }

  // GetDeployedYAML returns the production version's ultrabase.yaml, or ("", nil) if never deployed.
  func (s *ProjectService) GetDeployedYAML(ctx context.Context, userID, projectID string) (string, error) {
  	h := s.handler

  	var app utils.App
  	filter := bson.M{"_id": projectID, "Owner": userID}
  	if err := h.MongoDB.ReadOne(ctx, utils.COLLECTION_APPS, &app, filter); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return "", errNotFound("project not found")
  		}
  		return "", fmt.Errorf("get app: %w", err)
  	}

  	var versions []utils.Version
  	if err := h.MongoDB.Read(ctx, utils.COLLECTION_VERSIONS, &versions, bson.M{"AppId": projectID}); err != nil {
  		return "", fmt.Errorf("list versions: %w", err)
  	}

  	var prodVersion *utils.Version
  	for i := range versions {
  		if !versions[i].IsDraft {
  			prodVersion = &versions[i]
  			break
  		}
  	}
  	if prodVersion == nil {
  		return "", nil // never deployed
  	}

  	var prodDefs utils.Defs
  	if err := h.MongoDB.ReadOne(ctx, utils.COLLECTION_DEFS, &prodDefs, bson.M{"VersionId": prodVersion.Id}); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return "", nil
  		}
  		return "", fmt.Errorf("get defs: %w", err)
  	}
  	return prodDefs.Ultrabase, nil
  }

  // UploadYAML saves a new ultrabase.yaml to the project's draft Defs.
  func (s *ProjectService) UploadYAML(ctx context.Context, userID, projectID, yamlContent string) error {
  	// Ownership check
  	var app utils.App
  	filter := bson.M{
  		"_id":   projectID,
  		"Owner": userID,
  		"$or": []bson.M{
  			{"Deleted": false},
  			{"Deleted": bson.M{"$exists": false}},
  		},
  	}
  	if err := s.handler.MongoDB.ReadOne(ctx, utils.COLLECTION_APPS, &app, filter); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return errNotFound("project not found")
  		}
  		return fmt.Errorf("get app: %w", err)
  	}

  	draftVersion, err := s.handler.ensureDraftVersion(ctx, projectID)
  	if err != nil {
  		return fmt.Errorf("ensure draft: %w", err)
  	}

  	now := time.Now()
  	var existingDefs utils.Defs
  	defsFilter := bson.M{"VersionId": draftVersion.Id}
  	defsErr := s.handler.MongoDB.ReadOne(ctx, utils.COLLECTION_DEFS, &existingDefs, defsFilter)
  	if defsErr != nil && defsErr != mongo.ErrNoDocuments {
  		return fmt.Errorf("read defs: %w", defsErr)
  	}
  	if defsErr == mongo.ErrNoDocuments {
  		newDefs := utils.Defs{
  			Id:        bson.NewObjectID().Hex(),
  			VersionId: draftVersion.Id,
  			Ultrabase: yamlContent,
  			Pages:     make(map[string]utils.PageDef),
  			Charts:    make(map[string]utils.ChartDef),
  			Config:    &utils.Config{},
  			CreatedAt: now,
  			UpdatedAt: now,
  		}
  		return s.handler.MongoDB.Create(ctx, utils.COLLECTION_DEFS, newDefs)
  	}
  	_, err = s.handler.MongoDB.UpdateOne(ctx, utils.COLLECTION_DEFS, defsFilter,
  		bson.M{"$set": bson.M{"Ultrabase": yamlContent, "UpdatedAt": now}})
  	return err
  }

  // MigrationPreview returns the migration diff between draft and production YAML.
  func (s *ProjectService) MigrationPreview(ctx context.Context, userID, projectID string) (*MigrationPreviewResult, error) {
  	h := s.handler
  	var app utils.App
  	if err := h.MongoDB.ReadOne(ctx, utils.COLLECTION_APPS, &app, bson.M{"_id": projectID, "Owner": userID}); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return nil, errNotFound("project not found")
  		}
  		return nil, fmt.Errorf("get app: %w", err)
  	}

  	var versions []utils.Version
  	if err := h.MongoDB.Read(ctx, utils.COLLECTION_VERSIONS, &versions, bson.M{"AppId": projectID}); err != nil {
  		return nil, fmt.Errorf("list versions: %w", err)
  	}

  	var draftVersion, prodVersion *utils.Version
  	for i := range versions {
  		if versions[i].IsDraft {
  			draftVersion = &versions[i]
  		} else {
  			prodVersion = &versions[i]
  		}
  	}
  	if draftVersion == nil {
  		return nil, fmt.Errorf("no draft version found")
  	}
  	if prodVersion == nil {
  		return nil, fmt.Errorf("no production version found")
  	}

  	var draftDefs, prodDefs utils.Defs
  	if err := h.MongoDB.ReadOne(ctx, utils.COLLECTION_DEFS, &draftDefs, bson.M{"VersionId": draftVersion.Id}); err != nil {
  		return nil, fmt.Errorf("get draft defs: %w", err)
  	}
  	_ = h.MongoDB.ReadOne(ctx, utils.COLLECTION_DEFS, &prodDefs, bson.M{"VersionId": prodVersion.Id})
  	if prodDefs.Pages == nil {
  		prodDefs.Pages = make(map[string]utils.PageDef)
  	}

  	preview := calculateMigration(draftDefs, prodDefs)
  	// calculateMigration returns a struct with a Diff string; adapt as needed.
  	// If calculateMigration returns a gin.H, marshal it to a string.
  	diffJSON, err := jsonMarshal(preview)
  	if err != nil {
  		return nil, fmt.Errorf("marshal preview: %w", err)
  	}
  	return &MigrationPreviewResult{Diff: string(diffJSON)}, nil
  }

  // DeployProject triggers a production deployment for the given project.
  func (s *ProjectService) DeployProject(ctx context.Context, userID, projectID string) (*DeployResult, error) {
  	h := s.handler
  	var app utils.App
  	if err := h.MongoDB.ReadOne(ctx, utils.COLLECTION_APPS, &app, bson.M{"_id": projectID, "Owner": userID}); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return nil, errNotFound("project not found")
  		}
  		return nil, fmt.Errorf("get app: %w", err)
  	}

  	var versions []utils.Version
  	if err := h.MongoDB.Read(ctx, utils.COLLECTION_VERSIONS, &versions, bson.M{"AppId": projectID}); err != nil {
  		return nil, fmt.Errorf("list versions: %w", err)
  	}

  	var draftVersion, prodVersion *utils.Version
  	for i := range versions {
  		if versions[i].IsDraft {
  			draftVersion = &versions[i]
  		} else {
  			prodVersion = &versions[i]
  		}
  	}
  	if draftVersion == nil {
  		return nil, fmt.Errorf("no draft version found")
  	}
  	if prodVersion == nil {
  		return nil, fmt.Errorf("no production version found")
  	}

  	// Copy draft defs to production.
  	var draftDefs utils.Defs
  	if err := h.MongoDB.ReadOne(ctx, utils.COLLECTION_DEFS, &draftDefs, bson.M{"VersionId": draftVersion.Id}); err != nil {
  		return nil, fmt.Errorf("get draft defs: %w", err)
  	}
  	if draftDefs.Ultrabase == "" && len(draftDefs.Pages) == 0 {
  		return nil, fmt.Errorf("app has no content to deploy")
  	}

  	now := time.Now()
  	prodUpdate := bson.M{
  		"Ultrabase": draftDefs.Ultrabase,
  		"Pages":     draftDefs.Pages,
  		"Charts":    draftDefs.Charts,
  		"Config":    draftDefs.Config,
  		"UpdatedAt": now,
  	}
  	if err := h.MongoDB.Update(ctx, utils.COLLECTION_DEFS, bson.M{"VersionId": prodVersion.Id}, prodUpdate, false); err != nil {
  		return nil, fmt.Errorf("update prod defs: %w", err)
  	}
  	_ = h.MongoDB.Update(ctx, utils.COLLECTION_VERSIONS, bson.M{"_id": prodVersion.Id},
  		bson.M{"Status": "building", "UpdatedAt": now}, false)

  	// Publish generation job.
  	msgBody, err := jsonMarshal(utils.GenerationMessage{VersionID: prodVersion.Id})
  	if err != nil {
  		return nil, fmt.Errorf("marshal message: %w", err)
  	}
  	if err := publishWithRetry(h.Producer, utils.TOPIC_GENERATION, msgBody); err != nil {
  		log.Printf("deploy %s: NSQ publish: %v", projectID, err)
  		return nil, fmt.Errorf("failed to enqueue deploy job")
  	}
  	return &DeployResult{VersionID: prodVersion.Id}, nil
  }

  // --- helpers ---

  type notFoundError struct{ msg string }

  func (e *notFoundError) Error() string { return e.msg }
  func errNotFound(msg string) error     { return &notFoundError{msg: msg} }
  func isNotFound(err error) bool {
  	_, ok := err.(*notFoundError)
  	return ok
  }

  func appToSummary(a *utils.App) *ProjectSummary {
  	return &ProjectSummary{
  		ID:        a.Id,
  		Name:      a.Name,
  		Slug:      a.Slug,
  		Domain:    a.GenericDomain,
  		Status:    a.Status,
  		CreatedAt: a.CreatedAt,
  	}
  }

  // jsonMarshal wraps json.Marshal for use within this package.
  func jsonMarshal(v any) ([]byte, error) {
  	return json.Marshal(v)
  }
  ```

  Add `"encoding/json"` to imports.

- [ ] **Step 4: Run tests to verify they pass**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestProjectService" -v
  ```
  Expected: PASS.

- [ ] **Step 5: Refactor ultrabase_handlers.go to use ProjectService**

  Replace the inline logic in each existing handler with a `ProjectService` call. The handler's job becomes: extract params from gin context → call service → map error to HTTP status → write response.

  Replace the body of `UltrabaseWhoamiHandler`:
  ```go
  func (h *DataHandler) UltrabaseWhoamiHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	result, err := (&ProjectService{handler: h}).Whoami(c.Request.Context(), userIDStr)
  	if err != nil {
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
  		return
  	}
  	c.JSON(http.StatusOK, result)
  }
  ```

  Replace the body of `CreateUltrabaseProjectHandler`:
  ```go
  func (h *DataHandler) CreateUltrabaseProjectHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	var req struct {
  		Name string `json:"name" binding:"required,min=1,max=100"`
  	}
  	if err := c.ShouldBindJSON(&req); err != nil {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required (1-100 chars)"})
  		return
  	}
  	result, err := (&ProjectService{handler: h}).CreateProject(c.Request.Context(), userIDStr, req.Name)
  	if err != nil {
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create project"})
  		return
  	}
  	c.JSON(http.StatusCreated, result)
  }
  ```

  Replace the body of `UltrabaseGetYAMLHandler`:
  ```go
  func (h *DataHandler) UltrabaseGetYAMLHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	yaml, err := (&ProjectService{handler: h}).GetDeployedYAML(c.Request.Context(), userIDStr, c.Param("id"))
  	if err != nil {
  		if isNotFound(err) {
  			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
  			return
  		}
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve yaml"})
  		return
  	}
  	if yaml == "" {
  		c.JSON(http.StatusOK, gin.H{"yaml": "", "empty": true})
  		return
  	}
  	c.JSON(http.StatusOK, gin.H{"yaml": yaml, "empty": false})
  }
  ```

  Replace the body of `UltrabaseUploadYAMLHandler`:
  ```go
  func (h *DataHandler) UltrabaseUploadYAMLHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	var req struct {
  		YAML string `json:"yaml" binding:"required"`
  	}
  	if err := c.ShouldBindJSON(&req); err != nil {
  		c.JSON(http.StatusBadRequest, gin.H{"error": "yaml is required"})
  		return
  	}
  	if err := (&ProjectService{handler: h}).UploadYAML(c.Request.Context(), userIDStr, c.Param("id"), req.YAML); err != nil {
  		if isNotFound(err) {
  			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
  			return
  		}
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save yaml"})
  		return
  	}
  	c.JSON(http.StatusOK, gin.H{"ok": true})
  }
  ```

  Replace the bodies of `UltrabaseDeployHandler` and `UltrabaseMigrationPreviewHandler`:
  ```go
  func (h *DataHandler) UltrabaseDeployHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	result, err := (&ProjectService{handler: h}).DeployProject(c.Request.Context(), userIDStr, c.Param("id"))
  	if err != nil {
  		if isNotFound(err) {
  			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
  			return
  		}
  		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
  		return
  	}
  	c.JSON(http.StatusOK, result)
  }

  func (h *DataHandler) UltrabaseMigrationPreviewHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	result, err := (&ProjectService{handler: h}).MigrationPreview(c.Request.Context(), userIDStr, c.Param("id"))
  	if err != nil {
  		if isNotFound(err) {
  			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
  			return
  		}
  		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
  		return
  	}
  	c.JSON(http.StatusOK, result)
  }
  ```

- [ ] **Step 6: Build to verify no compile errors**

  ```bash
  cd instancez-coder/v2/data && go build ./...
  ```
  Expected: no output.

- [ ] **Step 7: Run all data service tests**

  ```bash
  cd instancez-coder/v2/data && go test ./... -v
  ```
  Expected: all existing tests PASS (behaviour unchanged by refactor).

- [ ] **Step 8: Commit**

  ```bash
  git add data/pkg/server/project_service.go data/pkg/server/project_service_test.go data/pkg/server/ultrabase_handlers.go
  git commit -m "refactor(data): extract ProjectService; REST handlers become thin wrappers"
  ```

---

## Task 6: Add list/get/delete REST endpoints

**Files:**
- Modify: `instancez-coder/v2/data/pkg/server/ultrabase_handlers.go`
- Modify: `instancez-coder/v2/data/pkg/server/handler.go`

- [ ] **Step 1: Add handler tests**

  Append to `data/pkg/server/project_service_test.go`:

  ```go
  func TestProjectServiceListAndDelete(t *testing.T) {
  	// This test verifies the service methods exist and have correct signatures.
  	// Integration tests against a real MongoDB are in a separate build-tagged file.
  	svc := &ProjectService{handler: &DataHandler{}}
  	_ = svc.ListProjects   // must compile
  	_ = svc.DeleteProject  // must compile
  	_ = svc.GetProject     // must compile
  }
  ```

- [ ] **Step 2: Run the test**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestProjectServiceListAndDelete" -v
  ```
  Expected: PASS (all three methods already exist from Task 5).

- [ ] **Step 3: Add REST handler functions**

  Append to `data/pkg/server/ultrabase_handlers.go`:

  ```go
  func (h *DataHandler) UltrabaseListProjectsHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	projects, err := (&ProjectService{handler: h}).ListProjects(c.Request.Context(), userIDStr)
  	if err != nil {
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list projects"})
  		return
  	}
  	c.JSON(http.StatusOK, gin.H{"projects": projects})
  }

  func (h *DataHandler) UltrabaseGetProjectHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	project, err := (&ProjectService{handler: h}).GetProject(c.Request.Context(), userIDStr, c.Param("id"))
  	if err != nil {
  		if isNotFound(err) {
  			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
  			return
  		}
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get project"})
  		return
  	}
  	c.JSON(http.StatusOK, project)
  }

  func (h *DataHandler) UltrabaseDeleteProjectHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)
  	if userIDStr == "" {
  		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
  		return
  	}
  	if err := (&ProjectService{handler: h}).DeleteProject(c.Request.Context(), userIDStr, c.Param("id")); err != nil {
  		if isNotFound(err) {
  			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
  			return
  		}
  		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete project"})
  		return
  	}
  	c.JSON(http.StatusOK, gin.H{"ok": true})
  }
  ```

- [ ] **Step 4: Register new routes in handler.go**

  In `data/pkg/server/handler.go`, inside the `ultrabaseGroup` block, add:

  ```go
  ultrabaseGroup.GET("/projects", h.UltrabaseListProjectsHandler)
  ultrabaseGroup.GET("/projects/:id", h.UltrabaseGetProjectHandler)
  ultrabaseGroup.DELETE("/projects/:id", h.UltrabaseDeleteProjectHandler)
  ```

- [ ] **Step 5: Build and test**

  ```bash
  cd instancez-coder/v2/data && go build ./... && go test ./... -v
  ```
  Expected: build succeeds, all tests pass.

- [ ] **Step 6: Commit**

  ```bash
  git add data/pkg/server/ultrabase_handlers.go data/pkg/server/handler.go
  git commit -m "feat(data): add list/get/delete project REST endpoints"
  ```

---

## Task 7: MCP server scaffold — initialize and tools/list

**Files:**
- Create: `instancez-coder/v2/data/pkg/server/mcp_handler.go`
- Create: `instancez-coder/v2/data/pkg/server/mcp_handler_test.go`
- Modify: `instancez-coder/v2/data/pkg/server/handler.go`

- [ ] **Step 1: Write failing tests**

  Create `data/pkg/server/mcp_handler_test.go`:

  ```go
  package server

  import (
  	"encoding/json"
  	"net/http"
  	"net/http/httptest"
  	"strings"
  	"testing"

  	"github.com/gin-gonic/gin"
  	"github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  )

  func newMCPRequest(method, id any, params any) string {
  	m := map[string]any{"jsonrpc": "2.0", "method": method, "id": id}
  	if params != nil {
  		m["params"] = params
  	}
  	b, _ := json.Marshal(m)
  	return string(b)
  }

  func TestMCPInitialize(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := &DataHandler{}
  	r := gin.New()
  	r.Use(func(c *gin.Context) { c.Set("user_id", "user@example.com"); c.Next() })
  	r.POST("/mcp", h.MCPHandler)

  	body := newMCPRequest("initialize", 1, map[string]any{
  		"protocolVersion": "2024-11-05",
  		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
  	})
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	require.Equal(t, http.StatusOK, w.Code)
  	var resp map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
  	assert.Equal(t, "2.0", resp["jsonrpc"])
  	assert.Equal(t, float64(1), resp["id"])
  	result := resp["result"].(map[string]any)
  	assert.NotEmpty(t, result["serverInfo"])
  }

  func TestMCPToolsList(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := &DataHandler{}
  	r := gin.New()
  	r.Use(func(c *gin.Context) { c.Set("user_id", "user@example.com"); c.Next() })
  	r.POST("/mcp", h.MCPHandler)

  	body := newMCPRequest("tools/list", 2, nil)
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	require.Equal(t, http.StatusOK, w.Code)
  	var resp map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
  	result := resp["result"].(map[string]any)
  	tools := result["tools"].([]any)
  	assert.GreaterOrEqual(t, len(tools), 10)
  }

  func TestMCPUnknownMethod(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := &DataHandler{}
  	r := gin.New()
  	r.Use(func(c *gin.Context) { c.Set("user_id", "user@example.com"); c.Next() })
  	r.POST("/mcp", h.MCPHandler)

  	body := newMCPRequest("unknown/method", 3, nil)
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	require.Equal(t, http.StatusOK, w.Code)
  	var resp map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
  	assert.NotNil(t, resp["error"])
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestMCP" -v
  ```
  Expected: FAIL — `MCPHandler` undefined.

- [ ] **Step 3: Create mcp_handler.go with scaffold**

  Create `data/pkg/server/mcp_handler.go`:

  ```go
  package server

  import (
  	"encoding/json"
  	"net/http"

  	"github.com/gin-gonic/gin"
  )

  // JSON-RPC 2.0 envelope types.
  type mcpRequest struct {
  	JSONRPC string          `json:"jsonrpc"`
  	Method  string          `json:"method"`
  	Params  json.RawMessage `json:"params,omitempty"`
  	ID      any             `json:"id"`
  }

  type mcpResponse struct {
  	JSONRPC string `json:"jsonrpc"`
  	ID      any    `json:"id"`
  	Result  any    `json:"result,omitempty"`
  	Error   *mcpError `json:"error,omitempty"`
  }

  type mcpError struct {
  	Code    int    `json:"code"`
  	Message string `json:"message"`
  }

  // MCP error codes (JSON-RPC 2.0 + MCP extensions).
  const (
  	mcpErrParse          = -32700
  	mcpErrMethodNotFound = -32601
  	mcpErrInvalidParams  = -32602
  	mcpErrInternal       = -32603
  )

  func mcpOK(id, result any) mcpResponse {
  	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
  }

  func mcpErr(id any, code int, msg string) mcpResponse {
  	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: msg}}
  }

  // mcpToolResult wraps a tool call result in the MCP content envelope.
  func mcpToolResult(data any) map[string]any {
  	text, _ := json.Marshal(data)
  	return map[string]any{
  		"content": []map[string]any{{"type": "text", "text": string(text)}},
  		"isError": false,
  	}
  }

  func mcpToolError(msg string) map[string]any {
  	return map[string]any{
  		"content": []map[string]any{{"type": "text", "text": msg}},
  		"isError": true,
  	}
  }

  // MCPHandler handles POST /mcp — the MCP 2025 Streamable HTTP transport entry point.
  // Authentication is handled by BearerAuthMiddleware before this handler runs.
  func (h *DataHandler) MCPHandler(c *gin.Context) {
  	userID, _ := c.Get("user_id")
  	userIDStr, _ := userID.(string)

  	var req mcpRequest
  	if err := c.ShouldBindJSON(&req); err != nil {
  		c.JSON(http.StatusOK, mcpErr(nil, mcpErrParse, "parse error"))
  		return
  	}

  	switch req.Method {
  	case "initialize":
  		c.JSON(http.StatusOK, mcpOK(req.ID, map[string]any{
  			"protocolVersion": "2024-11-05",
  			"capabilities":    map[string]any{"tools": map[string]any{}},
  			"serverInfo":      map[string]any{"name": "ultrabase-mcp", "version": "1.0.0"},
  		}))

  	case "notifications/initialized":
  		// Client notification — no response needed (return 202).
  		c.Status(http.StatusAccepted)

  	case "tools/list":
  		c.JSON(http.StatusOK, mcpOK(req.ID, map[string]any{"tools": mcpToolDefinitions()}))

  	case "tools/call":
  		h.handleToolCall(c, userIDStr, req)

  	default:
  		c.JSON(http.StatusOK, mcpErr(req.ID, mcpErrMethodNotFound, "method not found: "+req.Method))
  	}
  }

  // mcpToolDefinitions returns the JSON Schema definitions for all MCP tools.
  func mcpToolDefinitions() []map[string]any {
  	str := map[string]any{"type": "string"}
  	required := func(props map[string]any, required []string) map[string]any {
  		return map[string]any{"type": "object", "properties": props, "required": required}
  	}
  	onlyProjectID := required(map[string]any{"project_id": str}, []string{"project_id"})

  	return []map[string]any{
  		{
  			"name":        "whoami",
  			"description": "Returns the authenticated user's email and user ID.",
  			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
  		},
  		{
  			"name":        "list_projects",
  			"description": "Lists all Ultrabase projects owned by the authenticated user.",
  			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
  		},
  		{
  			"name":        "create_project",
  			"description": "Creates a new Ultrabase backend project.",
  			"inputSchema": required(map[string]any{"name": str}, []string{"name"}),
  		},
  		{
  			"name":        "get_project",
  			"description": "Returns details for a single project.",
  			"inputSchema": onlyProjectID,
  		},
  		{
  			"name":        "delete_project",
  			"description": "Soft-deletes a project.",
  			"inputSchema": onlyProjectID,
  		},
  		{
  			"name":        "get_yaml",
  			"description": "Returns the production ultrabase.yaml for a project.",
  			"inputSchema": onlyProjectID,
  		},
  		{
  			"name":        "upload_yaml",
  			"description": "Uploads a new ultrabase.yaml draft to the project.",
  			"inputSchema": required(map[string]any{"project_id": str, "yaml": str}, []string{"project_id", "yaml"}),
  		},
  		{
  			"name":        "migration_preview",
  			"description": "Returns the migration diff between the draft and production YAML.",
  			"inputSchema": onlyProjectID,
  		},
  		{
  			"name":        "deploy_project",
  			"description": "Triggers a production deployment for the project.",
  			"inputSchema": onlyProjectID,
  		},
  		{
  			"name":        "execute_sql",
  			"description": "Executes a raw SQL query against the project's production database. The role has DML privileges only (SELECT/INSERT/UPDATE/DELETE). DDL (CREATE/ALTER/DROP) is rejected at the database level. Results are capped at 1000 rows. Statement timeout is 10s.",
  			"inputSchema": required(map[string]any{"project_id": str, "sql": str}, []string{"project_id", "sql"}),
  		},
  	}
  }

  // handleToolCall dispatches a tools/call request to the appropriate handler.
  // Stub — tool implementations added in Task 8 and 9.
  func (h *DataHandler) handleToolCall(c *gin.Context, userID string, req mcpRequest) {
  	var params struct {
  		Name      string          `json:"name"`
  		Arguments json.RawMessage `json:"arguments"`
  	}
  	if err := json.Unmarshal(req.Params, &params); err != nil {
  		c.JSON(http.StatusOK, mcpErr(req.ID, mcpErrInvalidParams, "invalid params"))
  		return
  	}
  	c.JSON(http.StatusOK, mcpErr(req.ID, mcpErrMethodNotFound, "tool not implemented: "+params.Name))
  }
  ```

- [ ] **Step 4: Register MCP routes in handler.go**

  In `data/pkg/server/handler.go`, add a new route group after the `ultrabaseGroup` block:

  ```go
  // MCP server (Streamable HTTP transport, MCP 2025 spec).
  // Bearer-PAT authenticated — same middleware as the ultrabase CLI surface.
  mcpGroup := r.Group("/mcp")
  mcpGroup.Use(h.BearerAuthMiddleware())
  mcpGroup.POST("", h.MCPHandler)
  ```

- [ ] **Step 5: Run tests**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestMCP" -v
  ```
  Expected: all three MCP tests PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add data/pkg/server/mcp_handler.go data/pkg/server/mcp_handler_test.go data/pkg/server/handler.go
  git commit -m "feat(data): MCP server scaffold — initialize, tools/list, route registration"
  ```

---

## Task 8: Wire project management tools

**Files:**
- Modify: `instancez-coder/v2/data/pkg/server/mcp_handler.go`
- Modify: `instancez-coder/v2/data/pkg/server/mcp_handler_test.go`

- [ ] **Step 1: Add tool call tests**

  Append to `data/pkg/server/mcp_handler_test.go`:

  ```go
  func TestMCPWhoamiTool(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := &DataHandler{}
  	r := gin.New()
  	r.Use(func(c *gin.Context) { c.Set("user_id", "alice@example.com"); c.Next() })
  	r.POST("/mcp", h.MCPHandler)

  	body := newMCPRequest("tools/call", 4, map[string]any{
  		"name":      "whoami",
  		"arguments": map[string]any{},
  	})
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	require.Equal(t, http.StatusOK, w.Code)
  	var resp map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
  	result := resp["result"].(map[string]any)
  	assert.False(t, result["isError"].(bool))
  	content := result["content"].([]any)[0].(map[string]any)
  	assert.Contains(t, content["text"].(string), "alice@example.com")
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestMCPWhoamiTool" -v
  ```
  Expected: FAIL — tool returns "not implemented".

- [ ] **Step 3: Replace handleToolCall with full dispatch**

  Replace the `handleToolCall` function in `mcp_handler.go`:

  ```go
  func (h *DataHandler) handleToolCall(c *gin.Context, userID string, req mcpRequest) {
  	var params struct {
  		Name      string          `json:"name"`
  		Arguments json.RawMessage `json:"arguments"`
  	}
  	if err := json.Unmarshal(req.Params, &params); err != nil {
  		c.JSON(http.StatusOK, mcpErr(req.ID, mcpErrInvalidParams, "invalid params"))
  		return
  	}

  	ctx := c.Request.Context()
  	svc := &ProjectService{handler: h}

  	// Helper to unmarshal args into a struct.
  	args := func(dst any) bool {
  		if err := json.Unmarshal(params.Arguments, dst); err != nil {
  			c.JSON(http.StatusOK, mcpErr(req.ID, mcpErrInvalidParams, "invalid arguments: "+err.Error()))
  			return false
  		}
  		return true
  	}

  	switch params.Name {
  	case "whoami":
  		result, err := svc.Whoami(ctx, userID)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))

  	case "list_projects":
  		result, err := svc.ListProjects(ctx, userID)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))

  	case "create_project":
  		var a struct {
  			Name string `json:"name"`
  		}
  		if !args(&a) {
  			return
  		}
  		result, err := svc.CreateProject(ctx, userID, a.Name)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))

  	case "get_project":
  		var a struct {
  			ProjectID string `json:"project_id"`
  		}
  		if !args(&a) {
  			return
  		}
  		result, err := svc.GetProject(ctx, userID, a.ProjectID)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))

  	case "delete_project":
  		var a struct {
  			ProjectID string `json:"project_id"`
  		}
  		if !args(&a) {
  			return
  		}
  		if err := svc.DeleteProject(ctx, userID, a.ProjectID); err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(map[string]any{"ok": true})))

  	case "get_yaml":
  		var a struct {
  			ProjectID string `json:"project_id"`
  		}
  		if !args(&a) {
  			return
  		}
  		yaml, err := svc.GetDeployedYAML(ctx, userID, a.ProjectID)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(map[string]any{"yaml": yaml})))

  	case "upload_yaml":
  		var a struct {
  			ProjectID string `json:"project_id"`
  			YAML      string `json:"yaml"`
  		}
  		if !args(&a) {
  			return
  		}
  		if err := svc.UploadYAML(ctx, userID, a.ProjectID, a.YAML); err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(map[string]any{"ok": true})))

  	case "migration_preview":
  		var a struct {
  			ProjectID string `json:"project_id"`
  		}
  		if !args(&a) {
  			return
  		}
  		result, err := svc.MigrationPreview(ctx, userID, a.ProjectID)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))

  	case "deploy_project":
  		var a struct {
  			ProjectID string `json:"project_id"`
  		}
  		if !args(&a) {
  			return
  		}
  		result, err := svc.DeployProject(ctx, userID, a.ProjectID)
  		if err != nil {
  			c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  			return
  		}
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))

  	case "execute_sql":
  		// Implemented in Task 9.
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError("execute_sql not yet implemented")))

  	default:
  		c.JSON(http.StatusOK, mcpErr(req.ID, mcpErrMethodNotFound, "unknown tool: "+params.Name))
  	}
  }
  ```

- [ ] **Step 4: Run all MCP tests**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestMCP" -v
  ```
  Expected: all PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add data/pkg/server/mcp_handler.go data/pkg/server/mcp_handler_test.go
  git commit -m "feat(data): wire 9 MCP project management tools to ProjectService"
  ```

---

## Task 9: execute_sql tool

**Files:**
- Modify: `instancez-coder/v2/data/pkg/server/project_service.go`
- Modify: `instancez-coder/v2/data/pkg/server/mcp_handler.go`
- Modify: `instancez-coder/v2/data/pkg/server/mcp_handler_test.go`

- [ ] **Step 1: Add execute_sql test (rejects DDL)**

  Append to `data/pkg/server/mcp_handler_test.go`:

  ```go
  func TestMCPExecuteSQLMissingProjectID(t *testing.T) {
  	gin.SetMode(gin.TestMode)
  	h := &DataHandler{}
  	r := gin.New()
  	r.Use(func(c *gin.Context) { c.Set("user_id", "user@example.com"); c.Next() })
  	r.POST("/mcp", h.MCPHandler)

  	body := newMCPRequest("tools/call", 5, map[string]any{
  		"name":      "execute_sql",
  		"arguments": map[string]any{"sql": "SELECT 1"},
  		// no project_id
  	})
  	w := httptest.NewRecorder()
  	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
  	req.Header.Set("Content-Type", "application/json")
  	r.ServeHTTP(w, req)

  	require.Equal(t, http.StatusOK, w.Code)
  	var resp map[string]any
  	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
  	result := resp["result"].(map[string]any)
  	assert.True(t, result["isError"].(bool))
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  cd instancez-coder/v2/data && go test ./pkg/server/... -run "TestMCPExecuteSQL" -v
  ```
  Expected: FAIL — tool returns "not yet implemented" (isError=true but for wrong reason).

- [ ] **Step 3: Add ExecuteSQL to ProjectService**

  Append to `data/pkg/server/project_service.go` (add `"github.com/jackc/pgx/v5/pgxpool"` to imports):

  ```go
  // SQLResult holds the rows and column names from execute_sql.
  type SQLResult struct {
  	Columns []string        `json:"columns"`
  	Rows    [][]interface{} `json:"rows"`
  	RowCount int            `json:"row_count"`
  }

  // dbPoolEntry mirrors the deployer's db_user_pool collection entry.
  type dbPoolEntry struct {
  	DBName     string `bson:"db_name"`
  	DBUser     string `bson:"db_user"`
  	DBPassword string `bson:"db_password"`
  	Host       string `bson:"host"`
  	Port       int    `bson:"port"`
  }

  const (
  	sqlMaxRows        = 1000
  	sqlStatementTimeout = "10s"
  )

  // ExecuteSQL runs a raw SQL query against the project's production database.
  // The query runs under the service_role (DML only, no DDL). Results are capped
  // at sqlMaxRows rows. Statement timeout is sqlStatementTimeout.
  func (s *ProjectService) ExecuteSQL(ctx context.Context, userID, projectID, sql string) (*SQLResult, error) {
  	// Verify ownership.
  	if _, err := s.GetProject(ctx, userID, projectID); err != nil {
  		return nil, err
  	}

  	// Look up DB credentials from the pool.
  	var entry dbPoolEntry
  	filter := bson.M{"app_id": projectID, "status": "assigned"}
  	if err := s.handler.MongoDB.ReadOne(ctx, utils.COLLECTION_DB_USER_POOL, &entry, filter); err != nil {
  		if err == mongo.ErrNoDocuments {
  			return nil, fmt.Errorf("no database assigned to this project")
  		}
  		return nil, fmt.Errorf("look up db credentials: %w", err)
  	}

  	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=require",
  		entry.DBUser, entry.DBPassword, entry.Host, entry.Port, entry.DBName)

  	pool, err := pgxpool.New(ctx, connStr)
  	if err != nil {
  		return nil, fmt.Errorf("connect to project db: %w", err)
  	}
  	defer pool.Close()

  	// Apply statement timeout and run the query.
  	conn, err := pool.Acquire(ctx)
  	if err != nil {
  		return nil, fmt.Errorf("acquire connection: %w", err)
  	}
  	defer conn.Release()

  	if _, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%s'", sqlStatementTimeout)); err != nil {
  		return nil, fmt.Errorf("set statement_timeout: %w", err)
  	}

  	rows, err := conn.Query(ctx, sql)
  	if err != nil {
  		// Return Postgres errors verbatim so the LLM can self-correct.
  		return nil, err
  	}
  	defer rows.Close()

  	fields := rows.FieldDescriptions()
  	columns := make([]string, len(fields))
  	for i, f := range fields {
  		columns[i] = string(f.Name)
  	}

  	var result [][]interface{}
  	for rows.Next() {
  		if len(result) >= sqlMaxRows {
  			break
  		}
  		vals, err := rows.Values()
  		if err != nil {
  			return nil, fmt.Errorf("scan row: %w", err)
  		}
  		result = append(result, vals)
  	}
  	if err := rows.Err(); err != nil {
  		return nil, err
  	}

  	return &SQLResult{
  		Columns:  columns,
  		Rows:     result,
  		RowCount: len(result),
  	}, nil
  }
  ```

- [ ] **Step 4: Wire execute_sql in mcp_handler.go**

  In `mcp_handler.go`, replace the `execute_sql` case stub in `handleToolCall`:

  ```go
  case "execute_sql":
  	var a struct {
  		ProjectID string `json:"project_id"`
  		SQL       string `json:"sql"`
  	}
  	if !args(&a) {
  		return
  	}
  	if a.ProjectID == "" || a.SQL == "" {
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError("project_id and sql are required")))
  		return
  	}
  	result, err := svc.ExecuteSQL(ctx, userID, a.ProjectID, a.SQL)
  	if err != nil {
  		// Pass Postgres errors through verbatim for LLM self-correction.
  		c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolError(err.Error())))
  		return
  	}
  	c.JSON(http.StatusOK, mcpOK(req.ID, mcpToolResult(result)))
  ```

- [ ] **Step 5: Run all tests**

  ```bash
  cd instancez-coder/v2/data && go test ./... -v
  ```
  Expected: all PASS.

- [ ] **Step 6: Build both services**

  ```bash
  cd instancez-coder/v2/auth && go build ./... && cd ../data && go build ./...
  ```
  Expected: no errors.

- [ ] **Step 7: Commit**

  ```bash
  git add data/pkg/server/project_service.go data/pkg/server/mcp_handler.go data/pkg/server/mcp_handler_test.go
  git commit -m "feat(data): execute_sql MCP tool with statement timeout and row cap"
  ```

---

## Self-Review Notes

- **Spec coverage:** All 10 tools covered (9 project + execute_sql). OAuth discovery, register, authorize, token all covered. ProjectService extraction covered. New REST endpoints (list/get/delete) covered. MCP route registration covered. `EnsureOAuthIndexes` covered.
- **Type consistency:** `ProjectService`, `WhoamiResult`, `CreateResult`, `DeployResult`, `MigrationPreviewResult`, `SQLResult`, `notFoundError`, `mcpRequest`, `mcpResponse`, `mcpError` — all defined before use.
- **`calculateMigration`** is called in `MigrationPreview` — this function already exists in `deployment_handlers.go`. If it returns a non-JSON-serialisable type, `jsonMarshal` wraps it safely.
- **`utils.MongoClient.Update`** is used in `DeployProject` — verify its signature matches `func (c *MongoClient) Update(ctx, coll, filter, update, upsert)` before implementing.
- **`dbPoolEntry`** is defined locally in `project_service.go` — it duplicates the local `dbUserPool` struct in `apikey_handlers.go`. Consolidate by moving to `project_service.go` and removing from `apikey_handlers.go` as a cleanup step.
