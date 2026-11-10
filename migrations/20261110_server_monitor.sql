-- Back up the database and run BEFORE replacing controller/Agent binaries.
-- Uses only additive changes. Override @ppgo_prefix in the same connection.
SET @ppgo_prefix = COALESCE(@ppgo_prefix, 'pp_');
SET @server_name = CONCAT(@ppgo_prefix, 'task_server');
SET @server_table = CONCAT('`', REPLACE(@server_name, '`', '``'), '`');
SET @ddl = CONCAT('SELECT connection_type FROM ', @server_table, ' LIMIT 0');
PREPARE monitor_ddl FROM @ddl; EXECUTE monitor_ddl; DEALLOCATE PREPARE monitor_ddl;
SELECT COUNT(*) INTO @exists FROM information_schema.columns
 WHERE table_schema=DATABASE() AND table_name=@server_name AND column_name='monitor_alert_disabled';
SET @ddl = IF(@exists=0, CONCAT('ALTER TABLE ', @server_table,
 ' ADD COLUMN monitor_alert_disabled BOOL NOT NULL DEFAULT 0'), 'SELECT 1');
PREPARE monitor_ddl FROM @ddl; EXECUTE monitor_ddl; DEALLOCATE PREPARE monitor_ddl;

SET @ddl=CONCAT('CREATE TABLE IF NOT EXISTS `',REPLACE(CONCAT(@ppgo_prefix,'server_monitor_sample'),'`','``'),'` (
 id BIGINT AUTO_INCREMENT PRIMARY KEY, server_id INT NOT NULL, collected_at BIGINT NOT NULL,
 data TEXT NOT NULL, UNIQUE KEY server_time(server_id,collected_at), KEY sample_time(collected_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4');
PREPARE monitor_ddl FROM @ddl; EXECUTE monitor_ddl; DEALLOCATE PREPARE monitor_ddl;
SET @ddl=CONCAT('CREATE TABLE IF NOT EXISTS `',REPLACE(CONCAT(@ppgo_prefix,'server_monitor_config'),'`','``'),'` (
 id INT PRIMARY KEY, data TEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4');
PREPARE monitor_ddl FROM @ddl; EXECUTE monitor_ddl; DEALLOCATE PREPARE monitor_ddl;
SET @ddl=CONCAT('CREATE TABLE IF NOT EXISTS `',REPLACE(CONCAT(@ppgo_prefix,'server_monitor_state'),'`','``'),'` (
 id BIGINT AUTO_INCREMENT PRIMARY KEY, server_id INT NOT NULL, metric VARCHAR(16) NOT NULL,
 data TEXT NOT NULL, UNIQUE KEY server_metric(server_id,metric)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4');
PREPARE monitor_ddl FROM @ddl; EXECUTE monitor_ddl; DEALLOCATE PREPARE monitor_ddl;
SET @ddl=CONCAT('CREATE TABLE IF NOT EXISTS `',REPLACE(CONCAT(@ppgo_prefix,'server_monitor_event'),'`','``'),'` (
 id BIGINT AUTO_INCREMENT PRIMARY KEY, server_id INT NOT NULL, incident VARCHAR(48) NOT NULL,
 kind VARCHAR(16) NOT NULL, metric VARCHAR(16) NOT NULL, recipient VARCHAR(254) NOT NULL,
 recipient_key VARCHAR(64) NOT NULL, created_at BIGINT NOT NULL, closed_at BIGINT NOT NULL DEFAULT 0,
 status VARCHAR(16) NOT NULL, attempts INT NOT NULL DEFAULT 0, next_attempt BIGINT NOT NULL,
 suppress_recovery BOOL NOT NULL DEFAULT 0, subject VARCHAR(255) NOT NULL, body TEXT NOT NULL,
 last_error VARCHAR(255) NOT NULL DEFAULT '''',
 UNIQUE KEY incident_delivery(incident,kind,recipient_key), KEY server_id(server_id),
 KEY delivery_due(status,next_attempt), KEY closed_at(closed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4');
PREPARE monitor_ddl FROM @ddl; EXECUTE monitor_ddl; DEALLOCATE PREPARE monitor_ddl;
