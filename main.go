package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// App 表示应用的核心结构，负责管理日志、静态资源目录、数据库连接与路由配置
type App struct {
	logger    *log.Logger
	staticDir string
	db        *sql.DB
}

// NewApp 创建并返回一个新的应用实例，初始化日志器与静态资源目录
func NewApp() *App {
	logger := log.New(os.Stdout, "[task-board] ", log.LstdFlags|log.Lshortfile)
	staticDir := "web"
	app := &App{
		logger:    logger,
		staticDir: staticDir,
	}
	// 初始化 SQLite 数据库
	if err := app.initDB(); err != nil {
		logger.Fatalf("数据库初始化失败: %v", err)
	}
	return app
}

// routes 构建并返回 HTTP 路由表，注册 API 与静态资源处理器
func (a *App) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// 基础 API
	mux.HandleFunc("/api/health", a.handleHealth)
	// 看板任务 API
	mux.HandleFunc("/api/tasks", a.handleTasks)
	mux.HandleFunc("/api/tasks/", a.handleTaskItem)
	// 标签查询 API
	mux.HandleFunc("/api/tags", a.handleTags)

	// 静态资源与首页
	fs := http.FileServer(http.Dir(a.staticDir))
	mux.Handle("/", fs)
	return mux
}

// handleHealth 返回健康检查结果，用于容器与监控系统探测
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// 已移除 hello 与 notes 相关 demo 代码，保留健康检查与看板任务功能

// initDB 初始化并迁移 SQLite 数据库
func (a *App) initDB() error {
	// 创建数据目录
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	dbPath := filepath.Join(dataDir, "app.db")
	// 打开数据库（mattn/go-sqlite3 驱动名称为 "sqlite3"）
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	// 简单的连接检查
	if err := db.Ping(); err != nil {
		return err
	}
	a.db = db
	// 开启外键约束，确保删除任务时级联删除 task_tags
	if _, err := a.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("启用外键约束失败: %w", err)
	}
	// 迁移表
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL,
		archived INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS task_tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		tag TEXT NOT NULL,
		FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_task_tags_task ON task_tags(task_id);
	`
	if _, err := a.db.Exec(schema); err != nil {
		return err
	}
	return nil
}

// writeJSON 将对象编码为 JSON 并写入响应
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// 已移除与 notes 相关的接口与数据结构

// Task 表示看板中的任务实体
type Task struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Tags        []string  `json:"tags"`
	Archived    bool      `json:"archived"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// validStatus 检查任务状态是否有效
func validStatus(s string) bool {
	switch s {
	case "规划中", "进行中", "搁置中", "已完成":
		return true
	default:
		return false
	}
}

// handleTasks 处理任务的创建与列表
func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleTasksList(w, r)
	case http.MethodPost:
		a.handleTasksCreate(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleTasksList 返回任务列表，支持 archived 查询参数
func (a *App) handleTasksList(w http.ResponseWriter, r *http.Request) {
	archParam := r.URL.Query().Get("archived")
	archived := archParam == "1" || strings.ToLower(archParam) == "true"
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if archived {
		page := int64(1)
		size := int64(20)
		if p := strings.TrimSpace(r.URL.Query().Get("page")); p != "" {
			if v, err := parseInt64(p); err == nil && v > 0 {
				page = v
			}
		}
		if s := strings.TrimSpace(r.URL.Query().Get("page_size")); s != "" {
			if v, err := parseInt64(s); err == nil && v > 0 && v <= 200 {
				size = v
			}
		}
		offset := (page - 1) * size
		cond := "WHERE archived = ?"
		var args []any
		args = append(args, 1)
		if q != "" {
			cond += " AND (title LIKE ? OR description LIKE ? OR id IN (SELECT task_id FROM task_tags WHERE tag LIKE ?))"
			pat := "%" + q + "%"
			args = append(args, pat, pat, pat)
		}
		var total int64
		if err := a.db.QueryRow("SELECT COUNT(*) FROM tasks "+cond, args...).Scan(&total); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		argsList := append(args, size, offset)
		rows, err := a.db.Query(`
			SELECT id, title, description, status, archived, created_at, updated_at
			FROM tasks
			`+cond+`
			ORDER BY id DESC
			LIMIT ? OFFSET ?
		`, argsList...)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		var out []Task
		for rows.Next() {
			var t Task
			var created, updated string
			var archInt int
			if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &archInt, &created, &updated); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			t.Archived = archInt != 0
			tags, _ := a.fetchTags(t.ID)
			t.Tags = tags
			t.CreatedAt, _ = time.Parse(time.RFC3339, created)
			t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
			out = append(out, t)
		}
		hasMore := offset+int64(len(out)) < total
		writeJSON(w, http.StatusOK, map[string]any{
			"items":     out,
			"total":     total,
			"page":      page,
			"page_size": size,
			"has_more":  hasMore,
		})
		return
	}
	rows, err := a.db.Query(`
		SELECT id, title, description, status, archived, created_at, updated_at
		FROM tasks
		WHERE archived = 0
		ORDER BY id DESC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var created, updated string
		var archInt int
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &archInt, &created, &updated); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		t.Archived = archInt != 0
		tags, _ := a.fetchTags(t.ID)
		t.Tags = tags
		t.CreatedAt, _ = time.Parse(time.RFC3339, created)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// fetchTags 查询任务的标签
func (a *App) fetchTags(taskID int64) ([]string, error) {
	rows, err := a.db.Query(`SELECT tag FROM task_tags WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// handleTags 返回系统中已有的标签列表，支持 q 模糊查询
func (a *App) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var rows *sql.Rows
	var err error
	if q != "" {
		rows, err = a.db.Query(`SELECT DISTINCT tag FROM task_tags WHERE tag LIKE ? ORDER BY tag`, "%"+q+"%")
	} else {
		rows, err = a.db.Query(`SELECT DISTINCT tag FROM task_tags ORDER BY tag`)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		tags = append(tags, tag)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tags})
}

// handleTasksCreate 创建任务，默认状态为“规划中”
func (a *App) handleTasksCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
		return
	}
	now := time.Now().Format(time.RFC3339)
	res, err := a.db.Exec(`
		INSERT INTO tasks (title, description, status, archived, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?)
	`, body.Title, body.Description, "规划中", now, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	taskID, _ := res.LastInsertId()
	for _, tag := range body.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		_, _ = a.db.Exec(`INSERT INTO task_tags (task_id, tag) VALUES (?, ?)`, taskID, tag)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": taskID})
}

// handleTaskItem 处理单个任务的子路径操作，如 status、archive
func (a *App) handleTaskItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}
	idStr := parts[0]
	var id int64
	{
		var err error
		id, err = parseInt64(idStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "status":
		if r.Method != http.MethodPatch {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if !validStatus(body.Status) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid status"})
			return
		}
		now := time.Now().Format(time.RFC3339)
		if _, err := a.db.Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, body.Status, now, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": body.Status})
	case "archive":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		now := time.Now().Format(time.RFC3339)
		if _, err := a.db.Exec(`UPDATE tasks SET archived = 1, updated_at = ? WHERE id = ?`, now, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "archived": true})
	case "update":
		if r.Method != http.MethodPatch {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		// 解析可选字段
		var body struct {
			Title       *string  `json:"title"`
			Description *string  `json:"description"`
			Tags        []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		// 构造动态更新语句
		setParts := []string{}
		args := []any{}
		if body.Title != nil {
			if strings.TrimSpace(*body.Title) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
				return
			}
			setParts = append(setParts, "title = ?")
			args = append(args, *body.Title)
		}
		if body.Description != nil {
			setParts = append(setParts, "description = ?")
			args = append(args, *body.Description)
		}
		now := time.Now().Format(time.RFC3339)
		setParts = append(setParts, "updated_at = ?")
		args = append(args, now, id)
		if len(setParts) > 0 {
			q := `UPDATE tasks SET ` + strings.Join(setParts, ", ") + ` WHERE id = ?`
			if _, err := a.db.Exec(q, args...); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		// 更新标签（如果提供）
		if body.Tags != nil {
			if err := a.replaceTaskTags(id, body.Tags); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
	case "copy":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		// 读取原任务
		src, err := a.fetchTaskDetail(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// 创建副本（保持原状态，归档强制为 0）
		now := time.Now().Format(time.RFC3339)
		res, err := a.db.Exec(`
			INSERT INTO tasks (title, description, status, archived, created_at, updated_at)
			VALUES (?, ?, ?, 0, ?, ?)
		`, src.Title, src.Description, src.Status, now, now)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		newID, _ := res.LastInsertId()
		// 复制标签
		if err := a.replaceTaskTags(newID, src.Tags); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": newID})
	case "":
		// 支持 RESTful 删除：DELETE /api/tasks/{id}
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
			return
		}
		// 彻底删除任务（已启用外键，task_tags 将级联删除）
		if _, err := a.db.Exec(`DELETE FROM tasks WHERE id = ?`, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	case "restore":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		now := time.Now().Format(time.RFC3339)
		if _, err := a.db.Exec(`UPDATE tasks SET archived = 0, status = ?, updated_at = ? WHERE id = ?`, "规划中", now, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "archived": false, "status": "规划中"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
	}
}

// fetchTaskDetail 查询并返回单个任务的详细信息（含标签）
func (a *App) fetchTaskDetail(id int64) (Task, error) {
	var t Task
	var created, updated string
	var archInt int
	err := a.db.QueryRow(`
		SELECT id, title, description, status, archived, created_at, updated_at
		FROM tasks
		WHERE id = ?
	`, id).Scan(&t.ID, &t.Title, &t.Description, &t.Status, &archInt, &created, &updated)
	if err != nil {
		return t, err
	}
	t.Archived = archInt != 0
	t.CreatedAt, _ = time.Parse(time.RFC3339, created)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	tags, _ := a.fetchTags(id)
	t.Tags = tags
	return t, nil
}

// replaceTaskTags 将指定任务的标签替换为给定集合（先清空后插入）
func (a *App) replaceTaskTags(taskID int64, tags []string) error {
	if _, err := a.db.Exec(`DELETE FROM task_tags WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err := a.db.Exec(`INSERT INTO task_tags (task_id, tag) VALUES (?, ?)`, taskID, tag); err != nil {
			return err
		}
	}
	return nil
}

// parseInt64 将字符串解析为 int64
func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmtSscanf(s, &n)
	return n, err
}

// fmtSscanf 是对 fmt.Sscanf 的简单封装（便于无格式化导入）
func fmtSscanf(s string, out *int64) (int, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, &strconvNumError{Err: "invalid number"}
		}
		n = n*10 + int64(s[i]-'0')
	}
	*out = n
	return len(s), nil
}

// strconvNumError 表示数字解析错误
type strconvNumError struct{ Err string }

// Error 返回错误信息
func (e *strconvNumError) Error() string { return e.Err }

// boolToInt 将布尔值转换为 0/1
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// getEnv 读取环境变量，如果为空则返回默认值
func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// main 是应用入口，负责启动 HTTP 服务器并绑定路由
func main() {
	app := NewApp()
	addr := ":" + getEnv("PORT", "8080")

	srv := &http.Server{
		Addr:         addr,
		Handler:      app.routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	app.logger.Printf("HTTP 服务启动于 %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.logger.Fatalf("服务器启动失败: %v", err)
	}
}
