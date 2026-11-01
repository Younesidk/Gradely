package server

import (
	"os"
	"sort"
	"sync"
	"time"
)

const (
	storageCacheDuration = 5 * time.Minute
	rowOverhead          = 16 // record header + rowid + b-tree cell bookkeeping (rough)
	guidText             = 36 // ids are stored as TEXT
	realSize             = 8
)

type userStorage struct {
	UserID    string `json:"userId"`
	Bytes     int64  `json:"bytes"`
	Semesters int    `json:"semesters"`
	Modules   int    `json:"modules"`
	Formulas  int    `json:"formulas"`
}

type storageReport struct {
	CalculatedAt     time.Time              `json:"calculatedAt"`
	PerUser          map[string]userStorage `json:"-"`
	TotalEstimated   int64                  `json:"totalEstimatedBytes"`
	DatabaseFileSize int64                  `json:"databaseFileBytes"`
	WalFileSize      int64                  `json:"walFileBytes"`
}

// largest returns the ids of the n users using the most storage.
func (r *storageReport) largest(n int) []string {
	all := make([]userStorage, 0, len(r.PerUser))
	for _, u := range r.PerUser {
		all = append(all, u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Bytes > all[j].Bytes })
	ids := []string{}
	for i := 0; i < len(all) && i < n; i++ {
		ids = append(ids, all[i].UserID)
	}
	return ids
}

// storageCache holds the (cached) per-user storage estimate.
type storageCache struct {
	mu     sync.Mutex
	report *storageReport
}

func (s *storageCache) get(a *App, force bool) (*storageReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.report != nil && a.now().Sub(s.report.CalculatedAt) < storageCacheDuration {
		return s.report, nil
	}
	r, err := computeStorage(a)
	if err != nil {
		return nil, err
	}
	s.report = r
	return r, nil
}

type agg struct {
	UserID string
	Cnt    int64
	Bytes  int64
}

// computeStorage estimates approximate *logical* per-user storage. SQLite keeps everything in one file, so physical
// bytes cannot be attributed to users exactly; this sums the size of the values in each user-owned row plus a fixed
// per-row overhead.
func computeStorage(a *App) (*storageReport, error) {
	run := func(query string) ([]agg, error) {
		var rows []agg
		return rows, a.DB.Raw(query).Scan(&rows).Error
	}
	semesters, err := run(`SELECT user_id AS user_id, COUNT(*) AS cnt,
		SUM(16 + 2 * 36 + 4 + length(CAST(name AS BLOB)) + IFNULL(length(CAST(academic_year AS BLOB)), 0)
			+ IFNULL(length(start_date), 0) + IFNULL(length(end_date), 0) + 2 * 28) AS bytes
		FROM semesters GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	modules, err := run(`SELECT user_id AS user_id, COUNT(*) AS cnt,
		SUM(16 + 3 * 36 + CASE WHEN formula_id IS NULL THEN 0 ELSE 36 END
			+ length(CAST(name AS BLOB)) + IFNULL(length(CAST(code AS BLOB)), 0) + 7 * 8 + 2 + 4 + 28) AS bytes
		FROM modules GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	formulas, err := run(`SELECT user_id AS user_id, COUNT(*) AS cnt,
		SUM(16 + 2 * 36 + length(CAST(name AS BLOB)) + length(CAST(expression AS BLOB)) + 2 + 28) AS bytes
		FROM formulas GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	// The account record itself (name, email, password hash, flags, timestamps).
	accounts, err := run(`SELECT id AS user_id, 1 AS cnt,
		16 + 36 + length(CAST(user_name AS BLOB)) * 2 + length(CAST(email AS BLOB)) * 2
		+ length(CAST(password_hash AS BLOB)) + 5 * 28 + 12 AS bytes FROM users`)
	if err != nil {
		return nil, err
	}

	perUser := map[string]userStorage{}
	merge := func(rows []agg, f func(u *userStorage, r agg)) {
		for _, r := range rows {
			u := perUser[r.UserID]
			u.UserID = r.UserID
			u.Bytes += r.Bytes
			f(&u, r)
			perUser[r.UserID] = u
		}
	}
	merge(accounts, func(*userStorage, agg) {})
	merge(semesters, func(u *userStorage, r agg) { u.Semesters += int(r.Cnt) })
	merge(modules, func(u *userStorage, r agg) { u.Modules += int(r.Cnt) })
	merge(formulas, func(u *userStorage, r agg) { u.Formulas += int(r.Cnt) })

	var total int64
	for _, u := range perUser {
		total += u.Bytes
	}
	path := a.Cfg.DatabasePath()
	return &storageReport{CalculatedAt: a.now(), PerUser: perUser, TotalEstimated: total,
		DatabaseFileSize: fileSize(path), WalFileSize: fileSize(path + "-wal")}, nil
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}
