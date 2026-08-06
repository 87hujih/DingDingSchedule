CREATE TABLE IF NOT EXISTS `agent_write_ledgers` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `business_key` varchar(64) NOT NULL,
  `operation` varchar(64) NOT NULL,
  `write_effect` varchar(32) NOT NULL,
  `created_at` datetime(3) NOT NULL,
  `updated_at` datetime(3) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_agent_write_business_key` (`tenant_id`, `business_key`),
  KEY `idx_agent_write_operation_updated` (`operation`, `updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
