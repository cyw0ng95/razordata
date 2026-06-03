package EX

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

const (
	tagSelect      = 1
	tagInsert      = 2
	tagUpdate      = 3
	tagDelete      = 4
	tagCreateTable = 5
	tagDropTable   = 6

	tagNullLit   = 10
	tagNumberLit = 11
	tagFloatLit  = 12
	tagStringLit = 13
	tagBoolLit   = 14
	tagIdent     = 15
	tagParam     = 16
	tagStar      = 17
	tagUnary     = 18
	tagBinary    = 19
	tagFunc      = 20
	tagAgg       = 21
	tagList      = 22
	tagBetween   = 23
	tagCase      = 24
	tagIn        = 25
	tagAlias     = 26
	tagCast      = 27
	tagExists    = 28
	tagSubq      = 29
	tagQName     = 30
)

type enc struct {
	buf []byte
}

func (e *enc) writeUvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	e.buf = append(e.buf, tmp[:n]...)
}

func (e *enc) writeBool(b bool) {
	if b {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

func (e *enc) writeString(s string) {
	e.writeUvarint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *enc) writeOp(op int) {
	e.writeUvarint(uint64(LX.TokenType(op)))
}

func (e *enc) writeExpr(x PS.Expr) {
	if x == nil {
		e.buf = append(e.buf, 0)
		return
	}
	e.buf = append(e.buf, 1)
	switch v := x.(type) {
	case *PS.NumberLiteral:
		e.buf = append(e.buf, tagNumberLit)
		var tmp [binary.MaxVarintLen64]byte
		n := binary.PutVarint(tmp[:], v.Val)
		e.buf = append(e.buf, tmp[:n]...)
	case *PS.FloatLiteral:
		e.buf = append(e.buf, tagFloatLit)
		bits := uint64(v.Val)
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], bits)
		e.buf = append(e.buf, tmp[:]...)
	case *PS.StringLiteral:
		e.buf = append(e.buf, tagStringLit)
		e.writeString(v.Val)
	case *PS.BoolLiteral:
		e.buf = append(e.buf, tagBoolLit)
		e.writeBool(v.Val)
	case *PS.NullLiteral:
		e.buf = append(e.buf, tagNullLit)
	case *PS.Ident:
		e.buf = append(e.buf, tagIdent)
		e.writeString(v.Name)
	case *PS.QualifiedName:
		e.buf = append(e.buf, tagQName)
		e.writeString(v.Table)
		e.writeString(v.Name)
	case *PS.AliasedExpr:
		e.buf = append(e.buf, tagAlias)
		e.writeExpr(v.Expr)
		e.writeString(v.Alias)
	case *PS.Param:
		e.buf = append(e.buf, tagParam)
		e.writeUvarint(uint64(v.Index))
	case *PS.StarExpr:
		e.buf = append(e.buf, tagStar)
	case *PS.UnaryExpr:
		e.buf = append(e.buf, tagUnary)
		e.writeOp(v.Op)
		e.writeExpr(v.Operand)
	case *PS.BinaryExpr:
		e.buf = append(e.buf, tagBinary)
		e.writeOp(v.Op)
		e.writeExpr(v.Left)
		e.writeExpr(v.Right)
	case *PS.FunctionCall:
		e.buf = append(e.buf, tagFunc)
		e.writeString(v.Name)
		e.writeUvarint(uint64(len(v.Args)))
		for _, a := range v.Args {
			e.writeExpr(a)
		}
	case *PS.AggregateFunc:
		e.buf = append(e.buf, tagAgg)
		e.writeString(v.Name)
		e.writeExpr(v.Arg)
	case *PS.CastExpr:
		e.buf = append(e.buf, tagCast)
		e.writeExpr(v.Expr)
		e.writeUvarint(uint64(v.Type))
	case *PS.ListExpr:
		e.buf = append(e.buf, tagList)
		e.writeUvarint(uint64(len(v.Items)))
		for _, it := range v.Items {
			e.writeExpr(it)
		}
	case *PS.BetweenExpr:
		e.buf = append(e.buf, tagBetween)
		e.writeExpr(v.Expr)
		e.writeExpr(v.Low)
		e.writeExpr(v.High)
	case *PS.CaseExpr:
		e.buf = append(e.buf, tagCase)
		e.writeExpr(v.Expr)
		e.writeUvarint(uint64(len(v.WhenList)))
		for _, w := range v.WhenList {
			e.writeExpr(w.Cond)
			e.writeExpr(w.Then)
		}
		e.writeExpr(v.Else)
	case *PS.InExpr:
		e.buf = append(e.buf, tagIn)
		e.writeExpr(v.Expr)
		e.writeUvarint(uint64(len(v.List)))
		for _, it := range v.List {
			e.writeExpr(it)
		}
		if v.Subquery == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeStmt(v.Subquery)
		}
	case *PS.ExistsExpr:
		e.buf = append(e.buf, tagExists)
		if v.Subquery == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeStmt(v.Subquery)
		}
	case *PS.SubqueryExpr:
		e.buf = append(e.buf, tagSubq)
		if v.Subquery == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeStmt(v.Subquery)
		}
	}
}

func (e *enc) writeColDef(c PS.ColDef) {
	e.writeString(c.Name)
	e.writeUvarint(uint64(c.Type))
	e.writeUvarint(uint64(c.Size))
	e.writeBool(c.Nullable)
	e.writeBool(c.PK)
	e.writeBool(c.Unique)
	e.writeExpr(c.Default)
}

func (e *enc) writeStmt(s PS.Stmt) {
	if s == nil {
		e.buf = append(e.buf, 0)
		return
	}
	e.buf = append(e.buf, 1)
	switch v := s.(type) {
	case *PS.Select:
		e.buf = append(e.buf, tagSelect)
		e.writeBool(v.Distinct)
		e.writeUvarint(uint64(len(v.Cols)))
		for _, c := range v.Cols {
			e.writeExpr(c)
		}
		e.writeString(v.From)
		e.writeString(v.FromAlias)
		e.writeExpr(v.Where)
		e.writeUvarint(uint64(len(v.OrderBy)))
		for _, o := range v.OrderBy {
			e.writeExpr(o.Expr)
			e.writeBool(o.Desc)
		}
		e.writeExpr(v.Limit)
		e.writeExpr(v.Offset)
	case *PS.Insert:
		e.buf = append(e.buf, tagInsert)
		e.writeString(v.Table)
		e.writeUvarint(uint64(len(v.Cols)))
		for _, c := range v.Cols {
			e.writeString(c)
		}
		e.writeUvarint(uint64(len(v.Values)))
		for _, row := range v.Values {
			e.writeUvarint(uint64(len(row)))
			for _, cell := range row {
				e.writeExpr(cell)
			}
		}
	case *PS.Update:
		e.buf = append(e.buf, tagUpdate)
		e.writeString(v.Table)
		e.writeUvarint(uint64(len(v.Set)))
		for _, p := range v.Set {
			e.writeString(p.Col)
			e.writeExpr(p.Val)
		}
		e.writeExpr(v.Where)
	case *PS.Delete:
		e.buf = append(e.buf, tagDelete)
		e.writeString(v.Table)
		e.writeExpr(v.Where)
	case *PS.CreateTable:
		e.buf = append(e.buf, tagCreateTable)
		e.writeString(v.Name)
		e.writeUvarint(uint64(len(v.Cols)))
		for _, c := range v.Cols {
			e.writeColDef(c)
		}
		if v.PK == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeString(*v.PK)
		}
	case *PS.DropTable:
		e.buf = append(e.buf, tagDropTable)
		e.writeString(v.Name)
	}
}

func serializeKey(stmt PS.Stmt) string {
	e := &enc{}
	e.writeStmt(stmt)
	h := sha256.Sum256(e.buf)
	return string(h[:])
}
