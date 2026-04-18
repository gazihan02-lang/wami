package web

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"wami/templates"
)

const authUsername = "ender"

// bcrypt hash of "Ender123456."
var authPasswordHash = mustHash("Ender123456.")

func mustHash(pw string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}

// sessions stores active session tokens.
var (
	sessions   = map[string]time.Time{}
	sessionsMu sync.RWMutex
)

const sessionCookie = "wami_session"
const sessionMaxAge = 7 * 24 * 3600 // 7 days

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("auth: rand error: %v", err)
	}
	return hex.EncodeToString(b)
}

func createSession(w http.ResponseWriter) {
	token := generateToken()
	sessionsMu.Lock()
	sessions[token] = time.Now().Add(time.Duration(sessionMaxAge) * time.Second)
	sessionsMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func isAuthenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	sessionsMu.RLock()
	exp, ok := sessions[c.Value]
	sessionsMu.RUnlock()
	return ok && time.Now().Before(exp)
}

func destroySession(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err == nil {
		sessionsMu.Lock()
		delete(sessions, c.Value)
		sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:   sessionCookie,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

// authMiddleware redirects unauthenticated requests to /login.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow login page and static assets without auth
		if r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if !isAuthenticated(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	templates.Login("").Render(r.Context(), w)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username != authUsername || bcrypt.CompareHashAndPassword(authPasswordHash, []byte(password)) != nil {
		templates.Login("Kullanıcı adı veya şifre hatalı").Render(r.Context(), w)
		return
	}

	createSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	destroySession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
