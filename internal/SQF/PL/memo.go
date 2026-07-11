package PL

import (
	MM "github.com/cyw0ng95/razordata/internal/SQO/MM"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func SerializeKey(stmt PS.Stmt) string {
	return MM.SerializeKey(stmt, MM.DefaultSchemaVersion())
}

func SerializeKeyWithSchema(stmt PS.Stmt, schemaVersion uint64) string {
	return MM.SerializeKey(stmt, schemaVersion)
}

func BumpDefaultSchemaVersion() uint64 { return MM.BumpDefaultSchemaVersion() }

func SchemaVersion() uint64 { return MM.DefaultSchemaVersion() }

func NormalizeForMemo(stmt PS.Stmt) (PS.Stmt, []any) {
	return MM.NormalizeForMemo(stmt)
}