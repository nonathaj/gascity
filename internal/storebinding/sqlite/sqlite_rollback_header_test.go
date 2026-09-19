package sqlite

func sqliteRollbackHeaderForTest() []byte {
	header := make([]byte, 100)
	copy(header, "SQLite format 3\x00")
	header[18] = 1
	header[19] = 1
	return header
}
