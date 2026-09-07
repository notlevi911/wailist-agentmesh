package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

const authCookieName = "agentmesh_token"

// uiCookieName mirrors the frontend's own agentmesh_ui cookie (see
// useAuth.ts's setUICookie / middleware.ts) — a non-HttpOnly signal cookie
// the Next.js middleware gates protected routes on, since it can't read the
// HttpOnly agentmesh_token cookie set on the backend's cross-site response.
// Password sign-in/signup set it client-side right after a successful call,
// but OAuth is a pure server-redirect chain with no client JS in the loop,
// so OAuthCallback has to set it here or the middleware bounces a freshly
// signed-in OAuth user straight back to /signin.
const uiCookieName = "agentmesh_ui"

func (d *Deps) setUICookie(w http.ResponseWriter) {
	secure := strings.HasPrefix(os.Getenv("BASE_URL"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     uiCookieName,
		Value:    "1",
		Path:     "/",
		MaxAge:   int(tokenTTL.Seconds()),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// NativeClientHeader lets a non-browser client ask for its session as a bearer
// token instead of a cookie.
//
// The web app must keep using the HttpOnly cookie -- a token readable by page
// JavaScript is strictly weaker, since XSS can exfiltrate it. But a Capacitor
// WebView is not a browser tab: it runs on the https://localhost app origin,
// so the cookie set on the API's own domain is third-party and Android's
// WebView declines to send it by default. CORS_ORIGIN is a single origin too,
// so the cookie path cannot serve both clients at once.
//
// NewAuthMiddleware has always accepted "Authorization: Bearer" for exactly
// this kind of caller; the only thing missing was a way to obtain the token.
// This grants no new authority -- it is the same JWT the cookie already
// carries, with the same claims and lifetime -- it just hands it to a client
// that cannot use cookies. The app is responsible for storing it in
// Keystore-backed storage rather than anywhere a page can read it.
// Exported because middleware.go's CORS allow-list needs the same literal: a
// client cannot send a header the server's own CORS response doesn't permit,
// so the two must never drift.
const NativeClientHeader = "X-AgentMesh-Client"

// wantsBearerToken reports whether this caller identified itself as a native
// client. Opt-in by header rather than by user agent: a UA string is guessable
// and spoofable, and more to the point it changes under us, whereas a header
// our own app sets is an explicit request we control both ends of.
func wantsBearerToken(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get(NativeClientHeader))) {
	case "android", "ios":
		return true
	}
	return false
}

// authPayload builds the sign-in/sign-up response body. The cookie is set for
// every caller regardless; only a native client is additionally handed the raw
// token, so the browser response is byte-identical to what it was before.
func authPayload(r *http.Request, token string) map[string]any {
	if wantsBearerToken(r) {
		return map[string]any{"token": token}
	}
	return map[string]any{}
}

func (d *Deps) setAuthCookie(w http.ResponseWriter, token string) {
	secure := strings.HasPrefix(os.Getenv("BASE_URL"), "https")
	sameSite := http.SameSiteLaxMode
	if secure {
		// SameSite=None is required for cross-site cookies (different subdomain
		// frontend/backend); it must be paired with Secure.
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(tokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})
}

func (d *Deps) clearAuthCookie(w http.ResponseWriter) {
	secure := strings.HasPrefix(os.Getenv("BASE_URL"), "https")
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	})
}

// dummyHash is used in SignIn to keep response time constant even when the email doesn't exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-agentmesh"), bcrypt.DefaultCost)

const tokenTTL = 7 * 24 * time.Hour

type authClaims struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

func (d *Deps) SignUp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Org      string `json:"org"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Name = strings.TrimSpace(body.Name)
	body.Org = strings.TrimSpace(body.Org)
	if body.Email == "" || !strings.Contains(body.Email, "@") {
		respond.Error(w, http.StatusBadRequest, "valid email required")
		return
	}
	if len(body.Password) < 8 {
		respond.Error(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name required")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	user, err := d.Store.CreateUser(r.Context(), body.Email, string(hash))
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			respond.Error(w, http.StatusConflict, "email already registered")
			return
		}
		log.Printf("create user: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Password signup collects name/org up front, unlike OAuth (which only
	// gets a verified email from the provider) — persist them immediately
	// rather than sending this user through the OAuth onboarding prompt too.
	user, err = d.Store.UpdateProfile(r.Context(), user.ID, body.Name, body.Org)
	if err != nil {
		log.Printf("set profile on signup: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	token, err := d.issueToken(user)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	d.setAuthCookie(w, token)
	respond.JSON(w, http.StatusCreated, authPayload(r, token))
}

func (d *Deps) SignIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	if body.Email == "" || body.Password == "" {
		respond.Error(w, http.StatusBadRequest, "email and password required")
		return
	}

	user, lookupErr := d.Store.GetUserByEmail(r.Context(), body.Email)
	hash := []byte(user.PasswordHash)
	if lookupErr != nil {
		hash = dummyHash
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) != nil || lookupErr != nil {
		respond.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := d.issueToken(user)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	d.setAuthCookie(w, token)
	respond.JSON(w, http.StatusOK, authPayload(r, token))
}

func (d *Deps) SignOut(w http.ResponseWriter, r *http.Request) {
	d.clearAuthCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) Me(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	user, err := d.Store.GetUserByID(r.Context(), userID)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "not found")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"id":      user.ID,
		"email":   user.Email,
		"name":    user.Name,
		"orgName": user.OrgName,
		// OAuth accounts are created with no name — the frontend prompts for
		// name+org once, right after the provider redirect lands them here.
		"needsOnboarding": user.Name == "",
	})
}

// UpdateProfile sets the signed-in user's display name and organization
// name. Used by the post-OAuth onboarding prompt (see Me's needsOnboarding),
// and safe to call again later as a general profile edit.
func (d *Deps) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var body struct {
		Name    string `json:"name"`
		OrgName string `json:"orgName"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	body.Name = strings.TrimSpace(body.Name)
	body.OrgName = strings.TrimSpace(body.OrgName)
	if body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "name required")
		return
	}

	user, err := d.Store.UpdateProfile(r.Context(), userID, body.Name, body.OrgName)
	if err != nil {
		log.Printf("update profile: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"id":              user.ID,
		"email":           user.Email,
		"name":            user.Name,
		"orgName":         user.OrgName,
		"needsOnboarding": false,
	})
}

func (d *Deps) issueToken(user models.User) (string, error) {
	if len(d.JWTSecret) < 32 {
		return "", errors.New("jwt secret not configured")
	}
	claims := authClaims{
		UserID: user.ID,
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(d.JWTSecret))
}
