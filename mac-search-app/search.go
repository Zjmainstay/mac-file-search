package main

import (
	"strings"
)

// SearchOptions 搜索选项
type SearchOptions struct {
	Keyword    string   `json:"keyword"`
	UseRegex   bool     `json:"use_regex"`
	Extensions []string `json:"extensions"`  // 扩展名过滤，如 [".txt", ".log"]
	PathFilter string   `json:"path_filter"` // 路径过滤
	MinSize    int64    `json:"min_size"`    // 最小文件大小
	MaxSize    int64    `json:"max_size"`    // 最大文件大小
}

// SearchAdvanced 高级搜索
func (idx *Indexer) SearchAdvanced(opts SearchOptions) ([]FileEntry, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if opts.Keyword == "" {
		return []FileEntry{}, nil
	}

	// 构建查询
	var conditions []string
	var args []interface{}

	// 关键词搜索
	searchPattern := opts.Keyword
	if !opts.UseRegex {
		searchPattern = strings.ReplaceAll(searchPattern, "*", "%")
		searchPattern = strings.ReplaceAll(searchPattern, "?", "_")
		if !strings.Contains(opts.Keyword, "*") && !strings.Contains(opts.Keyword, "?") {
			searchPattern = "%" + searchPattern + "%"
		}
	} else {
		searchPattern = "%" + searchPattern + "%"
	}

	conditions = append(conditions, "(name LIKE ? OR path LIKE ?)")
	args = append(args, searchPattern, searchPattern)

	// 扩展名过滤
	if len(opts.Extensions) > 0 {
		placeholders := make([]string, len(opts.Extensions))
		for i, ext := range opts.Extensions {
			placeholders[i] = "?"
			args = append(args, strings.ToLower(ext))
		}
		conditions = append(conditions, "ext IN ("+strings.Join(placeholders, ",")+")")
	}

	// 路径过滤
	if opts.PathFilter != "" {
		conditions = append(conditions, "path LIKE ?")
		args = append(args, "%"+opts.PathFilter+"%")
	}

	// 文件大小过滤
	if opts.MinSize > 0 {
		conditions = append(conditions, "size >= ?")
		args = append(args, opts.MinSize)
	}
	if opts.MaxSize > 0 {
		conditions = append(conditions, "size <= ?")
		args = append(args, opts.MaxSize)
	}

	whereClause := strings.Join(conditions, " AND ")
	query := `SELECT id, path, name, size, mod_time, is_dir, ext
			  FROM files
			  WHERE ` + whereClause + `
			  ORDER BY
			    CASE
			      WHEN name LIKE ? THEN 0
			      ELSE 1
			    END,
			    is_dir DESC,
			    length(name),
			    name
			  LIMIT 500`

	exactPattern := opts.Keyword + "%"
	args = append(args, exactPattern)

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileEntry
	for rows.Next() {
		var entry FileEntry
		var isDir int
		err := rows.Scan(&entry.ID, &entry.Path, &entry.Name, &entry.Size, &entry.ModTime, &isDir, &entry.Ext)
		if err != nil {
			continue
		}
		entry.IsDir = isDir == 1
		results = append(results, entry)
	}

	return results, nil
}
