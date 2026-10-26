package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, err := sql.Open("pgx", os.Getenv("MOLA_PG_DSN"))
	if err != nil { fmt.Println("open:", err); return }
	defer db.Close()
	if err := db.Ping(); err != nil { fmt.Println("ping:", err); return }
	fmt.Println("ping OK")
	var n int
	_ = db.QueryRow("SELECT count(*) FROM memory_records").Scan(&n)
	fmt.Println("rows:", n)
}
