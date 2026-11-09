-- Run against a BACKED-UP v2.8.0 database, before starting the new application.
-- Set @ppgo_prefix to conf/app.conf db.prefix in the same connection if needed.
-- Do not use the mysql client's --force option: preflight errors should stop it.
SET @ppgo_prefix = COALESCE(@ppgo_prefix, 'pp_');
SET @task_name = CONCAT(@ppgo_prefix, 'task');
SET @log_name = CONCAT(@ppgo_prefix, 'task_log');
SET @task_table = CONCAT('`', REPLACE(@task_name, '`', '``'), '`');
SET @log_table = CONCAT('`', REPLACE(@log_name, '`', '``'), '`');

-- The old repository's initialization dump is not a complete v2.8.0 schema.
-- Check the actual production schema rather than importing that dump again.
SET @ddl = CONCAT('SELECT id, group_id, server_ids, create_id, status, create_time FROM ', @task_table, ' LIMIT 0');
PREPARE task_search_migration FROM @ddl;
EXECUTE task_search_migration;
DEALLOCATE PREPARE task_search_migration;
SET @ddl = CONCAT('SELECT id, task_id, server_id, create_time, status FROM ', @log_table, ' LIMIT 0');
PREPARE task_search_migration FROM @ddl;
EXECUTE task_search_migration;
DEALLOCATE PREPARE task_search_migration;

SELECT COUNT(*) INTO @exists FROM information_schema.columns
 WHERE table_schema=DATABASE() AND table_name=@task_name AND column_name='owner_id';
SET @ddl = IF(@exists=0, CONCAT('ALTER TABLE ', @task_table,
 ' ADD COLUMN owner_id INT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''Task owner, 0=unassigned'''), 'SELECT ''owner_id already exists''');
PREPARE task_search_migration FROM @ddl;
EXECUTE task_search_migration;
DEALLOCATE PREPARE task_search_migration;

SELECT COUNT(*) INTO @exists FROM information_schema.statistics
 WHERE table_schema=DATABASE() AND table_name=@task_name AND index_name='idx_task_search_owner';
SET @ddl = IF(@exists=0, CONCAT('ALTER TABLE ', @task_table,
 ' ADD INDEX idx_task_search_owner (owner_id)'), 'SELECT ''owner index already exists''');
PREPARE task_search_migration FROM @ddl;
EXECUTE task_search_migration;
DEALLOCATE PREPARE task_search_migration;

SELECT COUNT(*) INTO @exists FROM information_schema.statistics
 WHERE table_schema=DATABASE() AND table_name=@task_name AND index_name='idx_task_search_creator';
SET @ddl = IF(@exists=0, CONCAT('ALTER TABLE ', @task_table,
 ' ADD INDEX idx_task_search_creator (create_id)'), 'SELECT ''creator index already exists''');
PREPARE task_search_migration FROM @ddl;
EXECUTE task_search_migration;
DEALLOCATE PREPARE task_search_migration;

SELECT COUNT(*) INTO @exists FROM information_schema.statistics
 WHERE table_schema=DATABASE() AND table_name=@log_name AND index_name='idx_task_log_latest_server';
SET @ddl = IF(@exists=0, CONCAT('ALTER TABLE ', @log_table,
 ' ADD INDEX idx_task_log_latest_server (task_id, server_id, create_time, id)'), 'SELECT ''latest-log index already exists''');
PREPARE task_search_migration FROM @ddl;
EXECUTE task_search_migration;
DEALLOCATE PREPARE task_search_migration;
