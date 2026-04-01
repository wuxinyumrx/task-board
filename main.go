package main

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	// 定时任务 API
	mux.HandleFunc("/api/schedules", a.handleSchedules)
	mux.HandleFunc("/api/schedules/", a.handleScheduleItem)
	mux.HandleFunc("/api/schedules.run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		a.runScheduleOnce()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

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
	CREATE TABLE IF NOT EXISTS schedule_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		period TEXT NOT NULL,                        -- day, week, month, year
		week_day INTEGER,                            -- 0-6 (0=Sunday) for week
		month_day INTEGER,                           -- 1-31 for month/year
		month INTEGER,                               -- 1-12 for year
		start_date TEXT NOT NULL,                    -- YYYY-MM-DD
		end_date TEXT,                               -- YYYY-MM-DD or NULL (unlimited)
		last_run_date TEXT,                          -- YYYY-MM-DD; used to avoid duplicate generation for a day
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_schedule_tasks_period ON schedule_tasks(period);
	CREATE TABLE IF NOT EXISTS schedule_task_tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		schedule_id INTEGER NOT NULL,
		tag TEXT NOT NULL,
		FOREIGN KEY(schedule_id) REFERENCES schedule_tasks(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_schedule_task_tags_sid ON schedule_task_tags(schedule_id);
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

// ScheduleTask 表示定时任务的配置实体
type ScheduleTask struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Period      string    `json:"period"`        // day/week/month/year
	WeekDay     *int      `json:"week_day"`      // 1-7 前端表示；后端存 0-6
	MonthDay    *int      `json:"month_day"`     // 1-31
	Month       *int      `json:"month"`         // 1-12
	StartDate   string    `json:"start_date"`    // YYYY-MM-DD
	EndDate     *string   `json:"end_date"`      // YYYY-MM-DD or null
	LastRunDate *string   `json:"last_run_date"` // YYYY-MM-DD or null
	Tags        []string  `json:"tags"`          // 关联标签
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
		var ids []int64
		for rows.Next() {
			var t Task
			var created, updated string
			var archInt int
			if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &archInt, &created, &updated); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			t.Archived = archInt != 0
			ids = append(ids, t.ID)
			t.CreatedAt, _ = time.Parse(time.RFC3339, created)
			t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
			out = append(out, t)
		}
		if len(ids) > 0 {
			tagMap, _ := a.fetchTagsForIDs(ids)
			for i := range out {
				if v, ok := tagMap[out[i].ID]; ok {
					out[i].Tags = v
				}
			}
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
	var ids []int64
	for rows.Next() {
		var t Task
		var created, updated string
		var archInt int
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &archInt, &created, &updated); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		t.Archived = archInt != 0
		ids = append(ids, t.ID)
		t.CreatedAt, _ = time.Parse(time.RFC3339, created)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, t)
	}
	if len(ids) > 0 {
		tagMap, _ := a.fetchTagsForIDs(ids)
		for i := range out {
			if v, ok := tagMap[out[i].ID]; ok {
				out[i].Tags = v
			}
		}
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

// fetchTagsForIDs 批量查询任务标签，返回 taskID -> []tag 映射
func (a *App) fetchTagsForIDs(ids []int64) (map[int64][]string, error) {
	if len(ids) == 0 {
		return map[int64][]string{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT task_id, tag FROM task_tags WHERE task_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := a.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]string, len(ids))
	for rows.Next() {
		var tid int64
		var tag string
		if err := rows.Scan(&tid, &tag); err != nil {
			return nil, err
		}
		out[tid] = append(out[tid], tag)
	}
	return out, nil
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

// handleSchedules 处理定时任务的创建与列表
func (a *App) handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`
			SELECT id, name, period, week_day, month_day, month, start_date, end_date, last_run_date, created_at, updated_at
			FROM schedule_tasks
			ORDER BY id DESC
		`)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		var out []ScheduleTask
		for rows.Next() {
			var s ScheduleTask
			var weekDay, monthDay, month sql.NullInt64
			var endDate, lastRun sql.NullString
			var created, updated string
			if err := rows.Scan(&s.ID, &s.Name, &s.Period, &weekDay, &monthDay, &month, &s.StartDate, &endDate, &lastRun, &created, &updated); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if weekDay.Valid {
				v := int(weekDay.Int64) + 1
				s.WeekDay = &v
			}
			if monthDay.Valid {
				v := int(monthDay.Int64)
				s.MonthDay = &v
			}
			if month.Valid {
				v := int(month.Int64)
				s.Month = &v
			}
			if endDate.Valid {
				v := endDate.String
				s.EndDate = &v
			}
			if lastRun.Valid {
				v := lastRun.String
				s.LastRunDate = &v
			}
			if tags, err := a.fetchScheduleTags(s.ID); err == nil {
				s.Tags = tags
			}
			s.CreatedAt, _ = time.Parse(time.RFC3339, created)
			s.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
			out = append(out, s)
		}
		if out == nil {
			out = []ScheduleTask{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	case http.MethodPost:
		var body struct {
			Name      string   `json:"name"`
			Period    string   `json:"period"`     // day/week/month/year
			WeekDay   *int     `json:"week_day"`   // 1-7
			MonthDay  *int     `json:"month_day"`  // 1-31
			Month     *int     `json:"month"`      // 1-12
			StartDate string   `json:"start_date"` // YYYY-MM-DD
			EndDate   *string  `json:"end_date"`   // YYYY-MM-DD
			Tags      []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := validateScheduleInput(body.Name, body.Period, body.WeekDay, body.MonthDay, body.Month, body.StartDate, body.EndDate); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		now := time.Now().Format(time.RFC3339)
		var endDate sql.NullString
		if body.EndDate != nil && strings.TrimSpace(*body.EndDate) != "" {
			endDate = sql.NullString{String: *body.EndDate, Valid: true}
		}
		var weekDay sql.NullInt64
		if body.WeekDay != nil {
			// 存 0-6
			wd := int64((*body.WeekDay - 1 + 7) % 7)
			weekDay = sql.NullInt64{Int64: wd, Valid: true}
		}
		var monthDay sql.NullInt64
		if body.MonthDay != nil {
			monthDay = sql.NullInt64{Int64: int64(*body.MonthDay), Valid: true}
		}
		var month sql.NullInt64
		if body.Month != nil {
			month = sql.NullInt64{Int64: int64(*body.Month), Valid: true}
		}
		res, err := a.db.Exec(`
			INSERT INTO schedule_tasks (name, period, week_day, month_day, month, start_date, end_date, last_run_date, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)
		`, body.Name, strings.ToLower(body.Period), weekDay, monthDay, month, body.StartDate, endDate, now, now)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		newID, _ := res.LastInsertId()
		if err := a.replaceScheduleTags(newID, body.Tags); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// 立即尝试为该定时任务生成当日任务（命中则生成一次）
		_, _, _ = a.generateScheduleNow(newID)
		writeJSON(w, http.StatusCreated, map[string]any{"id": newID})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleScheduleItem 处理定时任务的查看、更新与删除
func (a *App) handleScheduleItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/schedules/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}
	id, err := parseInt64(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if action == "generate" {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		gen, taskID, err := a.generateScheduleNow(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !gen {
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "generated": false})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"schedule_id": id, "task_id": taskID})
		return
	}
	switch r.Method {
	case http.MethodGet:
		var s ScheduleTask
		var weekDay, monthDay, month sql.NullInt64
		var endDate, lastRun sql.NullString
		var created, updated string
		err := a.db.QueryRow(`
			SELECT id, name, period, week_day, month_day, month, start_date, end_date, last_run_date, created_at, updated_at
			FROM schedule_tasks WHERE id = ?
		`, id).Scan(&s.ID, &s.Name, &s.Period, &weekDay, &monthDay, &month, &s.StartDate, &endDate, &lastRun, &created, &updated)
		if err != nil {
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if weekDay.Valid {
			v := int(weekDay.Int64) + 1
			s.WeekDay = &v
		}
		if monthDay.Valid {
			v := int(monthDay.Int64)
			s.MonthDay = &v
		}
		if month.Valid {
			v := int(month.Int64)
			s.Month = &v
		}
		if endDate.Valid {
			v := endDate.String
			s.EndDate = &v
		}
		if lastRun.Valid {
			v := lastRun.String
			s.LastRunDate = &v
		}
		if tags, err := a.fetchScheduleTags(s.ID); err == nil {
			s.Tags = tags
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339, created)
		s.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		writeJSON(w, http.StatusOK, s)
	case http.MethodPatch:
		var body struct {
			Name      *string   `json:"name"`
			Period    *string   `json:"period"`
			WeekDay   *int      `json:"week_day"`
			MonthDay  *int      `json:"month_day"`
			Month     *int      `json:"month"`
			StartDate *string   `json:"start_date"`
			EndDate   *string   `json:"end_date"`
			Tags      *[]string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		// 读出旧值以验证新配置
		var cur ScheduleTask
		{
			var weekDay, monthDay, month sql.NullInt64
			var endDate, lastRun sql.NullString
			var created, updated string
			if err := a.db.QueryRow(`
				SELECT id, name, period, week_day, month_day, month, start_date, end_date, last_run_date, created_at, updated_at
				FROM schedule_tasks WHERE id = ?
			`, id).Scan(&cur.ID, &cur.Name, &cur.Period, &weekDay, &monthDay, &month, &cur.StartDate, &endDate, &lastRun, &created, &updated); err != nil {
				if err == sql.ErrNoRows {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if weekDay.Valid {
				v := int(weekDay.Int64) + 1
				cur.WeekDay = &v
			}
			if monthDay.Valid {
				v := int(monthDay.Int64)
				cur.MonthDay = &v
			}
			if month.Valid {
				v := int(month.Int64)
				cur.Month = &v
			}
			if endDate.Valid {
				v := endDate.String
				cur.EndDate = &v
			}
			if lastRun.Valid {
				v := lastRun.String
				cur.LastRunDate = &v
			}
		}
		newName := chooseStr(body.Name, cur.Name)
		newPeriod := chooseStr(body.Period, cur.Period)
		newWeekDay := chooseInt(body.WeekDay, cur.WeekDay)
		newMonthDay := chooseInt(body.MonthDay, cur.MonthDay)
		newMonth := chooseInt(body.Month, cur.Month)
		newStart := chooseStr(body.StartDate, cur.StartDate)
		var newEnd *string
		if body.EndDate != nil {
			val := strings.TrimSpace(*body.EndDate)
			newEnd = &val
		} else {
			newEnd = cur.EndDate
		}
		if err := validateScheduleInput(newName, newPeriod, newWeekDay, newMonthDay, newMonth, newStart, newEnd); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// 生成动态更新
		set := []string{}
		args := []any{}
		if body.Name != nil {
			set = append(set, "name = ?")
			args = append(args, newName)
		}
		if body.Period != nil {
			set = append(set, "period = ?")
			args = append(args, strings.ToLower(newPeriod))
		}
		if body.WeekDay != nil {
			if newWeekDay != nil {
				args = append(args, sql.NullInt64{Int64: int64((*newWeekDay - 1 + 7) % 7), Valid: true})
			} else {
				args = append(args, sql.NullInt64{Valid: false})
			}
			set = append(set, "week_day = ?")
		}
		if body.MonthDay != nil {
			if newMonthDay != nil {
				args = append(args, sql.NullInt64{Int64: int64(*newMonthDay), Valid: true})
			} else {
				args = append(args, sql.NullInt64{Valid: false})
			}
			set = append(set, "month_day = ?")
		}
		if body.Month != nil {
			if newMonth != nil {
				args = append(args, sql.NullInt64{Int64: int64(*newMonth), Valid: true})
			} else {
				args = append(args, sql.NullInt64{Valid: false})
			}
			set = append(set, "month = ?")
		}
		if body.StartDate != nil {
			set = append(set, "start_date = ?")
			args = append(args, newStart)
		}
		if body.EndDate != nil {
			if newEnd != nil && strings.TrimSpace(*newEnd) != "" {
				args = append(args, sql.NullString{String: *newEnd, Valid: true})
			} else {
				args = append(args, sql.NullString{Valid: false})
			}
			set = append(set, "end_date = ?")
		}
		now := time.Now().Format(time.RFC3339)
		set = append(set, "updated_at = ?")
		args = append(args, now, id)
		if len(set) > 0 {
			q := `UPDATE schedule_tasks SET ` + strings.Join(set, ", ") + ` WHERE id = ?`
			if _, err := a.db.Exec(q, args...); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		// 更新标签（如果提供）
		if body.Tags != nil {
			if err := a.replaceScheduleTags(id, *body.Tags); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
	case http.MethodDelete:
		if _, err := a.db.Exec(`DELETE FROM schedule_tasks WHERE id = ?`, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// fetchScheduleTags 查询定时任务的标签集合
func (a *App) fetchScheduleTags(scheduleID int64) ([]string, error) {
	rows, err := a.db.Query(`SELECT tag FROM schedule_task_tags WHERE schedule_id = ?`, scheduleID)
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

// replaceScheduleTags 用给定集合替换定时任务的标签（先清空再插入）
func (a *App) replaceScheduleTags(scheduleID int64, tags []string) error {
	if _, err := a.db.Exec(`DELETE FROM schedule_task_tags WHERE schedule_id = ?`, scheduleID); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err := a.db.Exec(`INSERT INTO schedule_task_tags (schedule_id, tag) VALUES (?, ?)`, scheduleID, tag); err != nil {
			return err
		}
	}
	return nil
}

// validateScheduleInput 校验定时任务配置
func validateScheduleInput(name, period string, weekDay, monthDay, month *int, startDate string, endDate *string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name required")
	}
	p := strings.ToLower(strings.TrimSpace(period))
	switch p {
	case "day":
		// no extra fields
	case "week":
		if weekDay == nil || *weekDay < 1 || *weekDay > 7 {
			return errors.New("week_day must be 1-7 for weekly period")
		}
	case "month":
		if monthDay == nil || *monthDay < 1 || *monthDay > 31 {
			return errors.New("month_day must be 1-31 for monthly period")
		}
	case "year":
		if month == nil || *month < 1 || *month > 12 {
			return errors.New("month must be 1-12 for yearly period")
		}
		if monthDay == nil || *monthDay < 1 || *monthDay > 31 {
			return errors.New("month_day must be 1-31 for yearly period")
		}
	default:
		return errors.New("invalid period")
	}
	if _, err := time.Parse("2006-01-02", startDate); err != nil {
		return errors.New("start_date must be YYYY-MM-DD")
	}
	if endDate != nil && strings.TrimSpace(*endDate) != "" {
		if _, err := time.Parse("2006-01-02", *endDate); err != nil {
			return errors.New("end_date must be YYYY-MM-DD")
		}
		if *endDate < startDate {
			return errors.New("end_date must be >= start_date")
		}
	}
	return nil
}

// chooseStr 返回首选非空字符串指针值
func chooseStr(p *string, cur string) string {
	if p != nil {
		return *p
	}
	return cur
}

// chooseInt 返回首选指针值或当前值
func chooseInt(p *int, cur *int) *int {
	if p != nil {
		return p
	}
	return cur
}

// startScheduler 启动定时器循环，每分钟检查一次“今天”是否应生成普通任务
func (a *App) startScheduler() {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		// 启动立即检查一次
		a.runScheduleOnce()
		for range ticker.C {
			a.runScheduleOnce()
		}
	}()
}

// runScheduleOnce 执行一次定时任务检查与任务生成
func (a *App) runScheduleOnce() {
	today := todayStr()
	// 只在 start<=today<=end 时考虑
	rows, err := a.db.Query(`
		SELECT id, name, period, week_day, month_day, month, start_date, end_date, last_run_date
		FROM schedule_tasks
		WHERE start_date <= ?
		  AND (end_date IS NULL OR end_date >= ?)
	`, today, today)
	if err != nil {
		a.logger.Printf("查询定时任务失败: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name, period, startDate string
		var weekDay, monthDay, month sql.NullInt64
		var endDate, lastRun sql.NullString
		if err := rows.Scan(&id, &name, &period, &weekDay, &monthDay, &month, &startDate, &endDate, &lastRun); err != nil {
			a.logger.Printf("扫描定时任务失败: %v", err)
			continue
		}
		if lastRun.Valid && lastRun.String == today {
			continue
		}
		match := false
		now := nowInTZ()
		switch strings.ToLower(period) {
		case "day":
			match = true
		case "week":
			if weekDay.Valid {
				// db: 0-6 Sunday=0; Go Weekday: Sunday=0
				got := int(now.Weekday())
				if got == int(weekDay.Int64) {
					match = true
				}
			}
		case "month":
			if monthDay.Valid && int(now.Day()) == int(monthDay.Int64) {
				match = true
			}
		case "year":
			if month.Valid && monthDay.Valid && int(now.Month()) == int(month.Int64) && now.Day() == int(monthDay.Int64) {
				match = true
			}
		default:
			match = false
		}
		if !match {
			continue
		}
		title := fmt.Sprintf("%s %s", name, today)
		nowStr := nowInTZ().Format(time.RFC3339)
		res, err := a.db.Exec(`
			INSERT INTO tasks (title, description, status, archived, created_at, updated_at)
			VALUES (?, ?, ?, 0, ?, ?)
		`, title, "", "规划中", nowStr, nowStr)
		if err != nil {
			a.logger.Printf("生成任务失败(schedule id=%d): %v", id, err)
			continue
		}
		newTaskID, _ := res.LastInsertId()
		// 为新生成任务附加定时任务的标签
		if tags, err := a.fetchScheduleTags(id); err == nil {
			if err := a.replaceTaskTags(newTaskID, tags); err != nil {
				a.logger.Printf("附加标签失败(task id=%d, schedule id=%d): %v", newTaskID, id, err)
			}
		}
		if _, err := a.db.Exec(`UPDATE schedule_tasks SET last_run_date = ?, updated_at = ? WHERE id = ?`, today, nowStr, id); err != nil {
			a.logger.Printf("更新定时任务 last_run_date 失败(id=%d): %v", id, err)
		}
	}
	// 自动归档已完成且未归档且超过10天的任务
	a.autoArchiveCompleted()
}

// autoArchiveCompleted 自动归档超过10天且状态为“已完成”的未归档任务
func (a *App) autoArchiveCompleted() {
	cutoff := nowInTZ().AddDate(0, 0, -10).Format(time.RFC3339)
	nowStr := nowInTZ().Format(time.RFC3339)
	_, err := a.db.Exec(`
		UPDATE tasks
		SET archived = 1, updated_at = ?
		WHERE archived = 0
		  AND status = ?
		  AND updated_at < ?
	`, nowStr, "已完成", cutoff)
	if err != nil {
		a.logger.Printf("自动归档任务失败: %v", err)
	}
}

// nowInTZ 返回考虑 TZ 环境变量的当前时间；若 TZ 不可用则返回本地时间
func nowInTZ() time.Time {
	tz := getEnv("TZ", "")
	if tz == "Asia/Shanghai" {
		loc := time.FixedZone("CST", 8*3600)
		return time.Now().In(loc)
	}
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return time.Now().In(loc)
		}
	}
	return time.Now()
}

// todayStr 返回当前日期字符串（YYYY-MM-DD），考虑 TZ
func todayStr() string {
	return nowInTZ().Format("2006-01-02")
}

// generateScheduleNow 尝试为指定定时任务在“今天”生成任务，返回是否生成、生成的任务ID与错误
func (a *App) generateScheduleNow(id int64) (bool, int64, error) {
	today := todayStr()
	var name, period, startDate string
	var weekDay, monthDay, month sql.NullInt64
	var endDate, lastRun sql.NullString
	err := a.db.QueryRow(`SELECT name, period, week_day, month_day, month, start_date, end_date, last_run_date FROM schedule_tasks WHERE id = ?`, id).
		Scan(&name, &period, &weekDay, &monthDay, &month, &startDate, &endDate, &lastRun)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, 0, fmt.Errorf("not found")
		}
		return false, 0, err
	}
	if !(startDate <= today && (!endDate.Valid || endDate.String >= today)) {
		return false, 0, fmt.Errorf("out of date range")
	}
	if lastRun.Valid && lastRun.String == today {
		return false, 0, nil
	}
	ok := false
	now := nowInTZ()
	switch strings.ToLower(period) {
	case "day":
		ok = true
	case "week":
		if weekDay.Valid && int(now.Weekday()) == int(weekDay.Int64) {
			ok = true
		}
	case "month":
		if monthDay.Valid && now.Day() == int(monthDay.Int64) {
			ok = true
		}
	case "year":
		if month.Valid && monthDay.Valid && int(now.Month()) == int(month.Int64) && now.Day() == int(monthDay.Int64) {
			ok = true
		}
	}
	if !ok {
		return false, 0, fmt.Errorf("not match today")
	}
	title := fmt.Sprintf("%s %s", name, today)
	nowStr := nowInTZ().Format(time.RFC3339)
	res, err := a.db.Exec(`INSERT INTO tasks (title, description, status, archived, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)`, title, "", "规划中", nowStr, nowStr)
	if err != nil {
		return false, 0, err
	}
	newTaskID, _ := res.LastInsertId()
	if tags, err := a.fetchScheduleTags(id); err == nil {
		_ = a.replaceTaskTags(newTaskID, tags)
	}
	_, _ = a.db.Exec(`UPDATE schedule_tasks SET last_run_date = ?, updated_at = ? WHERE id = ?`, today, nowStr, id)
	return true, newTaskID, nil
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

	// 启动定时任务调度器
	app.startScheduler()

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
