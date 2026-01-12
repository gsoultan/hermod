package mssql

const (
	queryResolveTableByID = `
		SELECT t.object_id, s.name, t.name 
		FROM sys.tables t 
		JOIN sys.schemas s ON t.schema_id = s.schema_id 
		WHERE t.object_id = OBJECT_ID(@p1)`

	queryResolveTableByFullQualifiedName = `
		SELECT t.object_id, s.name, t.name 
		FROM sys.tables t 
		JOIN sys.schemas s ON t.schema_id = s.schema_id 
		WHERE LOWER(s.name) = LOWER(@p1) AND LOWER(t.name) = LOWER(@p2)`

	queryResolveTableByNameOnly = `
		SELECT t.object_id, s.name, t.name 
		FROM sys.tables t 
		JOIN sys.schemas s ON t.schema_id = s.schema_id 
		WHERE LOWER(t.name) = LOWER(@p1) 
		ORDER BY CASE WHEN s.name = 'dbo' THEN 0 ELSE 1 END`

	queryGetMaxLSN = "SELECT sys.fn_cdc_get_max_lsn()"

	queryDiscoverCaptures = `
		SELECT s.name, t.name, ct.capture_instance, ct.source_object_id 
		FROM cdc.change_tables ct 
		JOIN sys.tables t ON ct.source_object_id = t.object_id 
		JOIN sys.schemas s ON t.schema_id = s.schema_id`

	queryCheckDatabaseCDC = "SELECT is_cdc_enabled FROM sys.databases WHERE name = DB_NAME()"

	queryEnableDatabaseCDC = "EXEC sys.sp_cdc_enable_db"

	queryCheckTableCDC = `
		SELECT 1 FROM sys.tables t 
		WHERE t.object_id = @p1 AND t.is_tracked_by_cdc = 1 
		AND EXISTS (SELECT 1 FROM cdc.change_tables WHERE source_object_id = t.object_id)`

	queryEnableTableCDC = "EXEC sys.sp_cdc_enable_table @source_schema = @p1, @source_name = @p2, @role_name = NULL, @supports_net_changes = 0"

	queryGetMinLSN = "SELECT sys.fn_cdc_get_min_lsn(@p1)"

	queryIncrementLSN = "SELECT sys.fn_cdc_increment_lsn(@p1)"

	queryGetTableChangesFormat = "SELECT * FROM cdc.fn_cdc_get_all_changes_%s(@p1, @p2, 'all')"
	queryDiscoverDatabases     = "SELECT name FROM sys.databases WHERE name NOT IN ('master', 'tempdb', 'model', 'msdb')"
	queryDiscoverTables        = "SELECT s.name + '.' + t.name FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id"
)
