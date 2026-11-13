package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var vaultPath string

// Служебные директории Obsidian/системы, которые не нужно обходить при поиске.
var skipDirs = map[string]bool{
	".obsidian": true,
	".trash":    true,
	".git":      true,
}

func main() {
	// 1. Получаем путь к хранилищу Obsidian из переменной окружения.
	vaultPath = os.Getenv("OBSIDIAN_VAULT_PATH")
	if vaultPath == "" {
		fmt.Fprintln(os.Stderr, "❌ Переменная окружения OBSIDIAN_VAULT_PATH не задана")
		os.Exit(1)
	}

	// Приводим путь к абсолютному для надёжной проверки безопасности.
	absVault, err := filepath.Abs(vaultPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Не удалось получить абсолютный путь: %v\n", err)
		os.Exit(1)
	}
	vaultPath = absVault

	// 2. Создаём MCP-сервер.
	s := server.NewMCPServer(
		"Obsidian Notes Server",
		"1.1.0",
		server.WithToolCapabilities(true),
	)

	// 3. Инструмент "search_notes" — поиск заметок по части имени файла.
	searchTool := mcp.NewTool("search_notes",
		mcp.WithDescription("Ищет заметки .md по части имени файла (без учёта регистра) и возвращает относительные пути. Используйте, когда не знаете точный путь."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Подстрока имени файла, например: 'вектор'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Максимум результатов (по умолчанию 50)"),
		),
	)
	s.AddTool(searchTool, handleSearchNotes)

	// 4. Инструмент "list_notes" — обзор содержимого папки.
	listTool := mcp.NewTool("list_notes",
		mcp.WithDescription("Показывает содержимое папки хранилища: подпапки и файлы .md. Используйте для навигации по структуре хранилища."),
		mcp.WithString("path",
			mcp.Description("Путь к папке относительно корня хранилища. Пустая строка — корень."),
		),
	)
	s.AddTool(listTool, handleListNotes)

	// 5. Инструмент "read_note" — чтение файла .md.
	readTool := mcp.NewTool("read_note",
		mcp.WithDescription("Читает содержимое заметки .md по её пути относительно хранилища Obsidian. Путь должен включать расширение .md. Если точный путь неизвестен, сначала вызовите search_notes."),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Путь к файлу относительно корня хранилища, например: 'Daily/2026-11-12.md'"),
		),
	)
	s.AddTool(readTool, handleReadNote)

	// 6. Инструмент "create_note" — создание или перезапись файла .md.
	createTool := mcp.NewTool("create_note",
		mcp.WithDescription("Создаёт новую заметку .md или полностью перезаписывает существующую"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Путь к файлу относительно корня хранилища"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Текстовое содержимое заметки в формате Markdown"),
		),
	)
	s.AddTool(createTool, handleCreateNote)

	// 7. Инструмент "edit_note" — точечное редактирование (замена текста).
	editTool := mcp.NewTool("edit_note",
		mcp.WithDescription("Редактирует существующую заметку .md, заменяя указанный фрагмент текста на новый"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Путь к файлу относительно корня хранилища"),
		),
		mcp.WithString("old_text",
			mcp.Required(),
			mcp.Description("Текст, который нужно найти и заменить"),
		),
		mcp.WithString("new_text",
			mcp.Required(),
			mcp.Description("Новый текст для вставки вместо old_text"),
		),
	)
	s.AddTool(editTool, handleEditNote)

	// 8. Запускаем сервер в режиме stdio (обмен JSON-RPC через stdin/stdout).
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Ошибка сервера: %v\n", err)
		os.Exit(1)
	}
}

// handleSearchNotes ищет .md файлы по части имени (без учёта регистра).
func handleSearchNotes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	limit := 50
	if v, err := req.RequireFloat("limit"); err == nil && v > 0 {
		limit = int(v)
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return mcp.NewToolResultError("query не может быть пустым"), nil
	}

	var matches []string

	err = filepath.WalkDir(vaultPath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // пропускаем недоступные файлы/папки
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if strings.Contains(strings.ToLower(d.Name()), q) {
			if rel, err := filepath.Rel(vaultPath, p); err == nil {
				matches = append(matches, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("ошибка поиска: %v", err)), nil
	}

	if len(matches) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("Ничего не найдено по запросу %q", query)), nil
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return mcp.NewToolResultText(strings.Join(matches, "\n")), nil
}

// handleListNotes показывает содержимое папки: подпапки и файлы .md.
func handleListNotes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, _ := req.RequireString("path") // пустая строка допустима — корень.

	fullPath, err := safeJoin(vaultPath, rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("не удалось прочитать папку: %v", err)), nil
	}

	var lines []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if skipDirs[name] {
				continue
			}
			lines = append(lines, name+"/")
		} else if strings.HasSuffix(strings.ToLower(name), ".md") {
			lines = append(lines, name)
		}
	}

	if len(lines) == 0 {
		return mcp.NewToolResultText("Папка пуста или не содержит .md файлов"), nil
	}
	return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
}

// handleReadNote читает файл .md и возвращает его содержимое.
// При отсутствии файла подсказывает похожие пути.
func handleReadNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	relPath, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fullPath, err := safeJoin(vaultPath, relPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			suggestions := findSimilarNotes(relPath, 5)
			if len(suggestions) > 0 {
				return mcp.NewToolResultError(fmt.Sprintf(
					"файл %q не найден. Возможно, вы имели в виду:\n%s",
					relPath, strings.Join(suggestions, "\n"),
				)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf(
				"файл %q не найден. Используйте инструмент search_notes, чтобы найти точный путь.",
				relPath,
			)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("не удалось прочитать файл: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// handleCreateNote создаёт или полностью перезаписывает файл .md.
func handleCreateNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	relPath, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fullPath, err := safeJoin(vaultPath, relPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Создаём родительские директории при необходимости.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("не удалось создать директории: %v", err)), nil
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("не удалось записать файл: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("✅ Заметка успешно сохранена: %s", relPath)), nil
}

// handleEditNote ищет old_text в файле и заменяет его на new_text.
func handleEditNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	relPath, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	oldText, err := req.RequireString("old_text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	newText, err := req.RequireString("new_text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fullPath, err := safeJoin(vaultPath, relPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			suggestions := findSimilarNotes(relPath, 5)
			if len(suggestions) > 0 {
				return mcp.NewToolResultError(fmt.Sprintf(
					"файл %q не найден. Возможно, вы имели в виду:\n%s",
					relPath, strings.Join(suggestions, "\n"),
				)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf(
				"файл %q не найден. Используйте инструмент search_notes, чтобы найти точный путь.",
				relPath,
			)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("не удалось прочитать файл: %v", err)), nil
	}

	original := string(data)
	if !strings.Contains(original, oldText) {
		return mcp.NewToolResultError("указанный для замены текст не найден в файле"), nil
	}

	updated := strings.Replace(original, oldText, newText, 1) // Заменяем только первое вхождение.
	if err := os.WriteFile(fullPath, []byte(updated), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("не удалось записать файл: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("✅ Заметка отредактирована: %s", relPath)), nil
}

// findSimilarNotes ищет .md файлы, имя которых похоже на запрошенный basename.
// Используется для подсказок при ошибке "файл не найден".
func findSimilarNotes(relPath string, limit int) []string {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(relPath), ".md"))
	if base == "" {
		return nil
	}

	var out []string
	_ = filepath.WalkDir(vaultPath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		name := strings.ToLower(strings.TrimSuffix(d.Name(), ".md"))
		// Эвристика: одно имя содержит другое (в любую сторону).
		if strings.Contains(name, base) || strings.Contains(base, name) {
			if rel, err := filepath.Rel(vaultPath, p); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		if len(out) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// safeJoin безопасно присоединяет relPath к базовой директории, предотвращая выход за её пределы.
func safeJoin(base, rel string) (string, error) {
	// Пустая строка — это корень хранилища (используется в list_notes).
	if strings.TrimSpace(rel) == "" {
		return base, nil
	}

	// Очищаем путь от лишних «.» и «..».
	cleanRel := filepath.Clean(rel)

	// Запрещаем абсолютные пути и попытки выхода вверх.
	if filepath.IsAbs(cleanRel) || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("недопустимый путь: %s", rel)
	}

	full := filepath.Join(base, cleanRel)

	// Финальная проверка: итоговый путь должен оставаться внутри базовой директории.
	relCheck, err := filepath.Rel(base, full)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("путь выходит за пределы хранилища: %s", rel)
	}

	return full, nil
}