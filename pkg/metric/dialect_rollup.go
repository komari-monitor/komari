package metric

// blobType returns the backend-specific column type for t-digest sketches.
func (sqliteDialect) blobType() string   { return "BLOB" }
func (mysqlDialect) blobType() string    { return "LONGBLOB" }
func (postgresDialect) blobType() string { return "BYTEA" }
