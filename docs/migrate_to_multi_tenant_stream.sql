-- 多租户 Stream 模式迁移脚本
-- 用途：将 dev.yaml 中的钉钉配置迁移到数据库

-- 1. 检查 tenants 表是否存在
-- 如果不存在，请先运行数据库迁移

-- 2. 插入乐知教育租户（从 dev.yaml 迁移）
INSERT INTO tenants (corp_id, name, app_key, app_secret, agent_id, status, created_at, updated_at)
VALUES (
  'dinge292658c9243df4235c2f4657eb6378f',  -- 企业 CorpID
  '乐知教育',                                -- 企业名称
  'dingvtmiaxzmsya4ymqs',                   -- AppKey
  'pJUpxMOzibe5hs7Zge3vB9hsnh_5b8_HXkA6MF0nc41QZBKHQUnH_HVA76t8yuP-',  -- AppSecret
  '4250011931',                             -- AgentID
  1,                                        -- 启用状态
  NOW(),
  NOW()
)
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  app_key = VALUES(app_key),
  app_secret = VALUES(app_secret),
  agent_id = VALUES(agent_id),
  status = VALUES(status),
  updated_at = NOW();

-- 3. 验证插入结果
SELECT id, corp_id, name, app_key, agent_id, status, created_at
FROM tenants
WHERE status = 1;

-- 4. 添加更多租户示例（根据实际情况修改）
-- INSERT INTO tenants (corp_id, name, app_key, app_secret, agent_id, status, created_at, updated_at)
-- VALUES (
--   'ding_your_corp_id_here',     -- 替换为实际的 CorpID
--   '你的企业名称',
--   'ding_your_app_key_here',     -- 替换为实际的 AppKey
--   'your_app_secret_here',       -- 替换为实际的 AppSecret
--   'your_agent_id_here',         -- 替换为实际的 AgentID
--   1,
--   NOW(),
--   NOW()
-- );

-- 5. 查询所有活跃租户（用于验证）
SELECT
  id AS '租户ID',
  corp_id AS '企业ID',
  name AS '企业名称',
  app_key AS 'AppKey',
  agent_id AS 'AgentID',
  CASE status
    WHEN 1 THEN '启用'
    WHEN 0 THEN '禁用'
    ELSE '未知'
  END AS '状态',
  created_at AS '创建时间',
  updated_at AS '更新时间'
FROM tenants
ORDER BY id;
