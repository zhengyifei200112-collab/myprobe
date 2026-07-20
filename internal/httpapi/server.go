package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"github.com/zhengyifei200112-collab/myprobe/internal/webui"
)

const sessionCookie = "myprobe_session"

type Server struct {
	config  config.Config
	store   *store.Store
	auth    *auth.Service
	gateway *agentgateway.Gateway
	hub     *agentgateway.Hub
	router  *gin.Engine
	handler http.Handler
}

func New(cfg config.Config, database *store.Store, authService *auth.Service, gateway *agentgateway.Gateway, hub *agentgateway.Hub) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), securityHeaders())
	server := &Server{config: cfg, store: database, auth: authService, gateway: gateway, hub: hub, router: router}
	server.routes()
	mux := http.NewServeMux()
	// WebSocket upgrades bypass Gin's wrapped ResponseWriter. coder/websocket uses
	// net/http hijacking directly, which avoids frame corruption through middleware wrappers.
	mux.HandleFunc("/api/v1/agent/ws", gateway.WebSocket)
	mux.HandleFunc("/api/v1/public/ws", server.publicWebSocket)
	ui := webui.NewHandler()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/api/") {
			router.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	}))
	server.handler = mux
	return server
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() {
	s.router.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := s.store.Health(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	public := s.router.Group("/api/v1/public")
	public.GET("/nodes", s.publicNodes)
	public.GET("/nodes/:nodeID/history", s.publicNodeHistory)

	s.router.POST("/api/v1/agent/report", gin.WrapF(s.gateway.HTTPReport))

	authRoutes := s.router.Group("/api/v1/auth")
	authRoutes.POST("/login", s.login)
	authRoutes.POST("/logout", s.requireSession(true), s.logout)
	authRoutes.GET("/me", s.requireSession(false), s.me)

	admin := s.router.Group("/api/v1/admin", s.requireSession(true))
	admin.GET("/nodes", s.adminNodes)
	admin.POST("/nodes", s.createNode)
	admin.PATCH("/nodes/:nodeID", s.updateNode)
	admin.DELETE("/nodes/:nodeID", s.deleteNode)
	admin.POST("/nodes/:nodeID/rotate-token", s.rotateNodeToken)
	admin.GET("/latency-config", s.latencyConfig)
	admin.POST("/targets", s.createTarget)
	admin.PATCH("/targets/:targetID", s.updateTarget)
	admin.DELETE("/targets/:targetID", s.deleteTarget)
	admin.POST("/target-groups", s.createTargetGroup)
	admin.PATCH("/target-groups/:groupID", s.updateTargetGroup)
	admin.DELETE("/target-groups/:groupID", s.deleteTargetGroup)
	admin.PUT("/target-groups/:groupID/targets/:targetID", s.addTargetToGroup)
	admin.DELETE("/target-groups/:groupID/targets/:targetID", s.removeTargetFromGroup)
	admin.PUT("/nodes/:nodeID/target-groups/:groupID", s.assignTargetGroup)
	admin.DELETE("/nodes/:nodeID/target-groups/:groupID", s.unassignTargetGroup)
}

func (s *Server) publicNodes(c *gin.Context) {
	nodes, err := s.store.ListPublicNodes(c.Request.Context(), time.Now().UTC())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list nodes"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"nodes": nodes, "server_time": time.Now().UTC()})
}

func (s *Server) publicNodeHistory(c *gin.Context) {
	rangeName := c.DefaultQuery("range", "1h")
	duration, bucket, ok := historyRange(rangeName)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range must be one of 1h, 12h, 1d, 3d, 7d, 30d"})
		return
	}
	start := time.Now().UTC().Add(-duration)
	metrics, err := s.store.MetricHistory(c.Request.Context(), c.Param("nodeID"), start, bucket)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read metric history"})
		return
	}
	latency, err := s.store.LatencyHistory(c.Request.Context(), c.Param("nodeID"), start, bucket)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read latency history"})
		return
	}
	traffic, err := s.store.TrafficHistory(c.Request.Context(), c.Param("nodeID"), start, time.Now().UTC(), bucket)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read traffic history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"range": rangeName, "bucket_seconds": bucket, "metrics": metrics, "latency": latency, "traffic": traffic})
}

func historyRange(name string) (time.Duration, int, bool) {
	switch name {
	case "1h":
		return time.Hour, 15, true
	case "12h":
		return 12 * time.Hour, 60, true
	case "1d":
		return 24 * time.Hour, 120, true
	case "3d":
		return 72 * time.Hour, 300, true
	case "7d":
		return 7 * 24 * time.Hour, 900, true
	case "30d":
		return 30 * 24 * time.Hour, 3600, true
	default:
		return 0, 0, false
	}
}

func (s *Server) publicWebSocket(w http.ResponseWriter, r *http.Request) {
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "connection closed")
	connection.SetReadLimit(4 << 10)
	ctx := connection.CloseRead(r.Context())

	nodes, err := s.store.ListPublicNodes(ctx, time.Now().UTC())
	if err != nil || wsjson.Write(ctx, connection, map[string]any{"type": "snapshot", "nodes": nodes}) != nil {
		return
	}
	events, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok || wsjson.Write(ctx, connection, event) != nil {
				return
			}
		}
	}
}

func (s *Server) login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	session, token, err := s.auth.Login(c.Request.Context(), request.Username, request.Password)
	if err != nil {
		time.Sleep(250 * time.Millisecond)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.config.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt,
	})
	c.JSON(http.StatusOK, gin.H{"csrf_token": session.CSRFToken, "expires_at": session.ExpiresAt})
}

func (s *Server) logout(c *gin.Context) {
	token, _ := c.Cookie(sessionCookie)
	_ = s.auth.Logout(c.Request.Context(), token)
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	c.Status(http.StatusNoContent)
}

func (s *Server) me(c *gin.Context) {
	sessionValue, _ := c.Get("session")
	session := sessionValue.(store.Session)
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "csrf_token": session.CSRFToken, "expires_at": session.ExpiresAt})
}

func (s *Server) createNode(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var request struct {
		ID                string   `json:"id"`
		Name              string   `json:"name"`
		Tags              []string `json:"tags"`
		CountryCode       string   `json:"country_code"`
		CollectionSeconds int      `json:"collection_seconds"`
		ReportSeconds     int      `json:"report_seconds"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	node, token, err := s.store.CreateNode(c.Request.Context(), store.CreateNodeParams{
		ID: request.ID, Name: request.Name, Tags: request.Tags, CountryCode: request.CountryCode,
		CollectionSeconds: request.CollectionSeconds, ReportSeconds: request.ReportSeconds,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"node": node, "agent_token": token})
	s.audit(c, "create", "node", node.ID, gin.H{"name": node.Name})
}

func (s *Server) adminNodes(c *gin.Context) {
	nodes, err := s.store.ListNodes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list nodes"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"nodes": nodes})
}

func (s *Server) updateNode(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var r struct {
		Name              string     `json:"name"`
		SortOrder         int        `json:"sort_order"`
		Hidden            bool       `json:"hidden"`
		Tags              []string   `json:"tags"`
		CountryCode       string     `json:"country_code"`
		Currency          string     `json:"currency"`
		PriceMinor        *int64     `json:"price_minor"`
		BillingCycle      string     `json:"billing_cycle"`
		ExpiresAt         *time.Time `json:"expires_at"`
		TrafficResetDay   *int       `json:"traffic_reset_day"`
		UseSinceBoot      bool       `json:"use_since_boot"`
		LatencyMode       string     `json:"latency_mode"`
		CollectionSeconds int        `json:"collection_seconds"`
		ReportSeconds     int        `json:"report_seconds"`
	}
	if json.NewDecoder(c.Request.Body).Decode(&r) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	node, err := s.store.UpdateNode(c.Request.Context(), c.Param("nodeID"), store.UpdateNodeParams{Name: r.Name, SortOrder: r.SortOrder, Hidden: r.Hidden, Tags: r.Tags, CountryCode: r.CountryCode, Currency: r.Currency, PriceMinor: r.PriceMinor, BillingCycle: r.BillingCycle, ExpiresAt: r.ExpiresAt, TrafficResetDay: r.TrafficResetDay, UseSinceBoot: r.UseSinceBoot, LatencyMode: r.LatencyMode, CollectionSeconds: r.CollectionSeconds, ReportSeconds: r.ReportSeconds})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "node", node.ID, r)
	c.JSON(http.StatusOK, gin.H{"node": node})
}
func (s *Server) deleteNode(c *gin.Context) {
	id := c.Param("nodeID")
	if s.store.DeleteNode(c.Request.Context(), id) != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	s.audit(c, "delete", "node", id, nil)
	c.Status(http.StatusNoContent)
}
func (s *Server) rotateNodeToken(c *gin.Context) {
	id := c.Param("nodeID")
	token, err := s.store.RotateAgentToken(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	s.audit(c, "rotate_token", "node", id, nil)
	c.JSON(http.StatusOK, gin.H{"agent_token": token})
}

func (s *Server) latencyConfig(c *gin.Context) {
	targets, err := s.store.ListTargets(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list targets"})
		return
	}
	groups, err := s.store.ListTargetGroups(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list groups"})
		return
	}
	assignments, err := s.store.ListTargetAssignments(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list assignments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"targets": targets, "groups": groups, "assignments": assignments})
}

func (s *Server) createTarget(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request struct {
		Name            string `json:"name"`
		Kind            string `json:"kind"`
		Host            string `json:"host"`
		Port            *int   `json:"port"`
		IntervalSeconds int    `json:"interval_seconds"`
		TimeoutMS       int    `json:"timeout_ms"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	target, err := s.store.CreateTarget(c.Request.Context(), store.CreateTargetParams{
		Name: request.Name, Kind: request.Kind, Host: request.Host, Port: request.Port,
		IntervalSeconds: request.IntervalSeconds, TimeoutMS: request.TimeoutMS,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"target": target})
	s.audit(c, "create", "target", target.ID, gin.H{"name": target.Name})
}

func (s *Server) updateTarget(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var r struct {
		Name            string `json:"name"`
		Kind            string `json:"kind"`
		Host            string `json:"host"`
		Port            *int   `json:"port"`
		IntervalSeconds int    `json:"interval_seconds"`
		TimeoutMS       int    `json:"timeout_ms"`
		Enabled         bool   `json:"enabled"`
		SortOrder       int    `json:"sort_order"`
	}
	if json.NewDecoder(c.Request.Body).Decode(&r) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	target, err := s.store.UpdateTarget(c.Request.Context(), c.Param("targetID"), store.UpdateTargetParams{Name: r.Name, Kind: r.Kind, Host: r.Host, Port: r.Port, IntervalSeconds: r.IntervalSeconds, TimeoutMS: r.TimeoutMS, Enabled: r.Enabled, SortOrder: r.SortOrder})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "target", target.ID, r)
	c.JSON(http.StatusOK, gin.H{"target": target})
}
func (s *Server) deleteTarget(c *gin.Context) {
	id := c.Param("targetID")
	if s.store.DeleteTarget(c.Request.Context(), id) != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
		return
	}
	s.audit(c, "delete", "target", id, nil)
	c.Status(http.StatusNoContent)
}

func (s *Server) createTargetGroup(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var request struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	group, err := s.store.CreateTargetGroup(c.Request.Context(), request.Name, request.Kind)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"group": group})
	s.audit(c, "create", "target_group", group.ID, gin.H{"name": group.Name})
}

func (s *Server) updateTargetGroup(c *gin.Context) {
	var r struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if json.NewDecoder(c.Request.Body).Decode(&r) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	group, err := s.store.UpdateTargetGroup(c.Request.Context(), c.Param("groupID"), r.Name, r.Kind)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "target_group", group.ID, r)
	c.JSON(http.StatusOK, gin.H{"group": group})
}
func (s *Server) deleteTargetGroup(c *gin.Context) {
	id := c.Param("groupID")
	if s.store.DeleteTargetGroup(c.Request.Context(), id) != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	s.audit(c, "delete", "target_group", id, nil)
	c.Status(http.StatusNoContent)
}

func (s *Server) addTargetToGroup(c *gin.Context) {
	if err := s.store.AddTargetToGroup(c.Request.Context(), c.Param("groupID"), c.Param("targetID")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "compatible target or group not found"})
		return
	}
	c.Status(http.StatusNoContent)
	s.audit(c, "attach_target", "target_group", c.Param("groupID"), gin.H{"target_id": c.Param("targetID")})
}
func (s *Server) removeTargetFromGroup(c *gin.Context) {
	if s.store.RemoveTargetFromGroup(c.Request.Context(), c.Param("groupID"), c.Param("targetID")) != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "assignment not found"})
		return
	}
	s.audit(c, "detach_target", "target_group", c.Param("groupID"), gin.H{"target_id": c.Param("targetID")})
	c.Status(http.StatusNoContent)
}

func (s *Server) assignTargetGroup(c *gin.Context) {
	if err := s.store.AssignTargetGroup(c.Request.Context(), c.Param("nodeID"), c.Param("groupID")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node or group not found"})
		return
	}
	c.Status(http.StatusNoContent)
	s.audit(c, "assign_group", "node", c.Param("nodeID"), gin.H{"group_id": c.Param("groupID")})
}
func (s *Server) unassignTargetGroup(c *gin.Context) {
	if s.store.UnassignTargetGroup(c.Request.Context(), c.Param("nodeID"), c.Param("groupID")) != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "assignment not found"})
		return
	}
	s.audit(c, "unassign_group", "node", c.Param("nodeID"), gin.H{"group_id": c.Param("groupID")})
	c.Status(http.StatusNoContent)
}

func (s *Server) audit(c *gin.Context, action, objectType, objectID string, details any) {
	value, ok := c.Get("session")
	if !ok {
		return
	}
	session := value.(store.Session)
	_ = s.store.LogAudit(c.Request.Context(), session.UserID, action, objectType, objectID, c.ClientIP(), details)
}

func (s *Server) requireSession(csrf bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookie)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		session, err := s.auth.Session(c.Request.Context(), token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
			return
		}
		if csrf && !safeMethod(c.Request.Method) {
			provided := c.GetHeader("X-CSRF-Token")
			if len(provided) != len(session.CSRFToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(session.CSRFToken)) != 1 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid CSRF token"})
				return
			}
		}
		c.Set("session", session)
		c.Next()
	}
}

func safeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:")
		c.Next()
	}
}
