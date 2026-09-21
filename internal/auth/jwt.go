package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"bell_server/internal/database"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrUnauthorized = errors.New("unauthorized access")
)

type Claims struct {
	UserID            int64  `json:"user_id"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	CanTriggerManual  bool   `json:"can_trigger_manual"`
	CanChangePreset   bool   `json:"can_change_preset"`
	CanSendTTS        bool   `json:"can_send_tts"`
	jwt.RegisteredClaims
}

// GetJWTSecret gets secret from DB or generates one
func GetJWTSecret() []byte {
	secret := database.GetSetting("jwt_secret", "")
	if secret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
		_ = database.SetSetting("jwt_secret", secret)
	}
	return []byte(secret)
}

// GenerateToken generates JWT with claims
func GenerateToken(userID int64, name, role string, trigger, preset, tts bool, duration time.Duration) (string, error) {
	claims := Claims{
		UserID:           userID,
		Name:             name,
		Role:             role,
		CanTriggerManual: trigger,
		CanChangePreset:  preset,
		CanSendTTS:       tts,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "BellPintarServer",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(GetJWTSecret())
}

// ValidateToken parses and validates token
func ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return GetJWTSecret(), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

// JWTMiddleware enforces auth on endpoints
func JWTMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header must be Bearer <token>"})
			c.Abort()
			return
		}

		claims, err := ValidateToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Save claims to context
		c.Set("claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("user_name", claims.Name)
		c.Set("user_role", claims.Role)

		c.Next()
	}
}

// RequireRole checks if user has one of allowed roles
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claimsVal, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		claims := claimsVal.(*Claims)
		allowed := false
		for _, r := range roles {
			if strings.EqualFold(claims.Role, r) {
				allowed = true
				break
			}
		}

		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access forbidden: insufficient role permissions"})
			c.Abort()
			return
		}

		c.Next()
	}
}
