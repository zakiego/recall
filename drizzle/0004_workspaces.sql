-- Users (Better Auth: username, passkey and organization plugins) and workspaces.
-- Hand-written: 0002 and 0003 land on other branches. See docs/multi-user.md.
--
-- Every data row gets a workspace_id. Existing rows all go to workspace ws_zaki
-- ("Zaki"), which is only created when the database already holds data.
-- workspace_id is added with ADD COLUMN instead of rebuilding the tables, so
-- columns added by 0002/0003 survive and no ON DELETE CASCADE fires. Triggers
-- make the '' default unusable.
CREATE TABLE `user` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`email` text NOT NULL,
	`email_verified` integer DEFAULT 0 NOT NULL,
	`image` text,
	`username` text,
	`display_username` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `user_email_unique` ON `user` (`email`);
--> statement-breakpoint
CREATE UNIQUE INDEX `user_username_unique` ON `user` (`username`);
--> statement-breakpoint
CREATE TABLE `session` (
	`id` text PRIMARY KEY NOT NULL,
	`expires_at` integer NOT NULL,
	`token` text NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	`ip_address` text,
	`user_agent` text,
	`user_id` text NOT NULL,
	`active_organization_id` text,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `session_token_unique` ON `session` (`token`);
--> statement-breakpoint
CREATE INDEX `session_user_id_idx` ON `session` (`user_id`);
--> statement-breakpoint
CREATE TABLE `account` (
	`id` text PRIMARY KEY NOT NULL,
	`account_id` text NOT NULL,
	`provider_id` text NOT NULL,
	`user_id` text NOT NULL,
	`access_token` text,
	`refresh_token` text,
	`id_token` text,
	`access_token_expires_at` integer,
	`refresh_token_expires_at` integer,
	`scope` text,
	`password` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `account_user_id_idx` ON `account` (`user_id`);
--> statement-breakpoint
CREATE TABLE `verification` (
	`id` text PRIMARY KEY NOT NULL,
	`identifier` text NOT NULL,
	`value` text NOT NULL,
	`expires_at` integer NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `verification_identifier_idx` ON `verification` (`identifier`);
--> statement-breakpoint
CREATE TABLE `passkey` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text,
	`public_key` text NOT NULL,
	`user_id` text NOT NULL,
	`credential_id` text NOT NULL,
	`counter` integer NOT NULL,
	`device_type` text NOT NULL,
	`backed_up` integer NOT NULL,
	`transports` text,
	`created_at` integer,
	`aaguid` text,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `passkey_user_id_idx` ON `passkey` (`user_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `passkey_credential_id_unique` ON `passkey` (`credential_id`);
--> statement-breakpoint
CREATE TABLE `organization` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`slug` text NOT NULL,
	`logo` text,
	`metadata` text,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `organization_slug_unique` ON `organization` (`slug`);
--> statement-breakpoint
CREATE TABLE `member` (
	`id` text PRIMARY KEY NOT NULL,
	`organization_id` text NOT NULL,
	`user_id` text NOT NULL,
	`role` text NOT NULL,
	`created_at` integer NOT NULL,
	FOREIGN KEY (`organization_id`) REFERENCES `organization`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `member_organization_user_unique` ON `member` (`organization_id`, `user_id`);
--> statement-breakpoint
CREATE INDEX `member_user_id_idx` ON `member` (`user_id`);
--> statement-breakpoint
CREATE TABLE `invitation` (
	`id` text PRIMARY KEY NOT NULL,
	`organization_id` text NOT NULL,
	`email` text NOT NULL,
	`role` text,
	`status` text NOT NULL,
	`expires_at` integer NOT NULL,
	`created_at` integer NOT NULL,
	`inviter_id` text NOT NULL,
	FOREIGN KEY (`organization_id`) REFERENCES `organization`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`inviter_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `invitation_organization_id_idx` ON `invitation` (`organization_id`);
--> statement-breakpoint
CREATE TABLE `workspace_invites` (
	`id` text PRIMARY KEY NOT NULL,
	`workspace_id` text NOT NULL,
	`role` text NOT NULL,
	`token_hash` text NOT NULL,
	`created_by` text,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`expires_at` integer NOT NULL,
	`accepted_at` integer,
	`accepted_by` text,
	`revoked_at` integer,
	FOREIGN KEY (`workspace_id`) REFERENCES `organization`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`created_by`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`accepted_by`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
CREATE UNIQUE INDEX `workspace_invites_token_hash_unique` ON `workspace_invites` (`token_hash`);
--> statement-breakpoint
CREATE INDEX `workspace_invites_workspace_idx` ON `workspace_invites` (`workspace_id`, `created_at`);
--> statement-breakpoint

-- Tenancy columns ---------------------------------------------------------------
ALTER TABLE `documents` ADD `workspace_id` text DEFAULT '' NOT NULL;
--> statement-breakpoint
ALTER TABLE `chunks` ADD `workspace_id` text DEFAULT '' NOT NULL;
--> statement-breakpoint
ALTER TABLE `memories` ADD `workspace_id` text DEFAULT '' NOT NULL;
--> statement-breakpoint
ALTER TABLE `jobs` ADD `workspace_id` text DEFAULT '' NOT NULL;
--> statement-breakpoint
ALTER TABLE `api_keys` ADD `workspace_id` text DEFAULT '' NOT NULL;
--> statement-breakpoint
ALTER TABLE `api_keys` ADD `created_by` text REFERENCES `user`(`id`) ON DELETE set null;
--> statement-breakpoint

-- The legacy workspace, only when there is something to put in it.
INSERT INTO `organization` (`id`, `name`, `slug`, `created_at`)
SELECT 'ws_zaki', 'Zaki', 'zaki', cast(unixepoch('subsec') * 1000 as integer)
WHERE EXISTS (SELECT 1 FROM `documents`) OR EXISTS (SELECT 1 FROM `memories`) OR EXISTS (SELECT 1 FROM `profiles`)
   OR EXISTS (SELECT 1 FROM `jobs`) OR EXISTS (SELECT 1 FROM `api_keys`);
--> statement-breakpoint
UPDATE `documents` SET `workspace_id` = 'ws_zaki';
--> statement-breakpoint
UPDATE `chunks` SET `workspace_id` = 'ws_zaki';
--> statement-breakpoint
UPDATE `memories` SET `workspace_id` = 'ws_zaki';
--> statement-breakpoint
UPDATE `jobs` SET `workspace_id` = 'ws_zaki';
--> statement-breakpoint
UPDATE `api_keys` SET `workspace_id` = 'ws_zaki';
--> statement-breakpoint

-- profiles: the primary key becomes (workspace_id, container). Nothing references it.
CREATE TABLE `profiles_new` (
	`workspace_id` text NOT NULL,
	`container` text NOT NULL,
	`static_text` text DEFAULT '' NOT NULL,
	`dynamic_text` text DEFAULT '' NOT NULL,
	`updated_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	PRIMARY KEY (`workspace_id`, `container`)
);
--> statement-breakpoint
INSERT INTO `profiles_new` (`workspace_id`, `container`, `static_text`, `dynamic_text`, `updated_at`)
SELECT 'ws_zaki', `container`, `static_text`, `dynamic_text`, `updated_at` FROM `profiles`;
--> statement-breakpoint
DROP TABLE `profiles`;
--> statement-breakpoint
ALTER TABLE `profiles_new` RENAME TO `profiles`;
--> statement-breakpoint

-- Indexes lead with workspace_id now.
DROP INDEX `documents_container_idx`;
--> statement-breakpoint
CREATE INDEX `documents_workspace_container_idx` ON `documents` (`workspace_id`, `container`, `created_at`);
--> statement-breakpoint
DROP INDEX `memories_current_idx`;
--> statement-breakpoint
CREATE INDEX `memories_current_idx` ON `memories` (`workspace_id`, `container`, `valid_to`, `created_at`);
--> statement-breakpoint
CREATE INDEX `chunks_workspace_idx` ON `chunks` (`workspace_id`, `container`);
--> statement-breakpoint
CREATE INDEX `api_keys_workspace_idx` ON `api_keys` (`workspace_id`, `created_at`);
--> statement-breakpoint

-- Search indexes get a workspace column. Copy out, rebuild, copy back, taking
-- workspace_id from the base row (which also drops index rows with no base row).
CREATE TABLE `_m_memories_fts` AS
SELECT f.`fact`, f.`keywords`, f.`memory_id`, m.`workspace_id`, m.`container`
FROM `memories_fts` f JOIN `memories` m ON m.`id` = f.`memory_id`;
--> statement-breakpoint
DROP TABLE `memories_fts`;
--> statement-breakpoint
CREATE VIRTUAL TABLE `memories_fts` USING fts5(
	fact,
	keywords,
	memory_id UNINDEXED,
	workspace_id UNINDEXED,
	container UNINDEXED,
	tokenize = 'porter unicode61 remove_diacritics 2'
);
--> statement-breakpoint
INSERT INTO `memories_fts` (`fact`, `keywords`, `memory_id`, `workspace_id`, `container`)
SELECT `fact`, `keywords`, `memory_id`, `workspace_id`, `container` FROM `_m_memories_fts`;
--> statement-breakpoint
DROP TABLE `_m_memories_fts`;
--> statement-breakpoint
CREATE TABLE `_m_chunks_fts` AS
SELECT f.`text`, f.`chunk_id`, c.`workspace_id`, c.`container`
FROM `chunks_fts` f JOIN `chunks` c ON c.`id` = f.`chunk_id`;
--> statement-breakpoint
DROP TABLE `chunks_fts`;
--> statement-breakpoint
CREATE VIRTUAL TABLE `chunks_fts` USING fts5(
	text,
	chunk_id UNINDEXED,
	workspace_id UNINDEXED,
	container UNINDEXED,
	tokenize = 'porter unicode61 remove_diacritics 2'
);
--> statement-breakpoint
INSERT INTO `chunks_fts` (`text`, `chunk_id`, `workspace_id`, `container`)
SELECT `text`, `chunk_id`, `workspace_id`, `container` FROM `_m_chunks_fts`;
--> statement-breakpoint
DROP TABLE `_m_chunks_fts`;
--> statement-breakpoint
CREATE TABLE `_m_memories_vec` AS
SELECT v.`memory_id`, m.`workspace_id`, m.`container`, v.`embedding`
FROM `memories_vec` v JOIN `memories` m ON m.`id` = v.`memory_id`;
--> statement-breakpoint
DROP TABLE `memories_vec`;
--> statement-breakpoint
CREATE VIRTUAL TABLE `memories_vec` USING vec0(
	memory_id text primary key,
	workspace_id text partition key,
	container text partition key,
	embedding float[384] distance_metric=cosine
);
--> statement-breakpoint
INSERT INTO `memories_vec` (`memory_id`, `workspace_id`, `container`, `embedding`)
SELECT `memory_id`, `workspace_id`, `container`, `embedding` FROM `_m_memories_vec`;
--> statement-breakpoint
DROP TABLE `_m_memories_vec`;
--> statement-breakpoint
CREATE TABLE `_m_chunks_vec` AS
SELECT v.`chunk_id`, c.`workspace_id`, c.`container`, v.`embedding`
FROM `chunks_vec` v JOIN `chunks` c ON c.`id` = v.`chunk_id`;
--> statement-breakpoint
DROP TABLE `chunks_vec`;
--> statement-breakpoint
CREATE VIRTUAL TABLE `chunks_vec` USING vec0(
	chunk_id text primary key,
	workspace_id text partition key,
	container text partition key,
	embedding float[384] distance_metric=cosine
);
--> statement-breakpoint
INSERT INTO `chunks_vec` (`chunk_id`, `workspace_id`, `container`, `embedding`)
SELECT `chunk_id`, `workspace_id`, `container`, `embedding` FROM `_m_chunks_vec`;
--> statement-breakpoint
DROP TABLE `_m_chunks_vec`;
--> statement-breakpoint

-- A row without a workspace is a bug. Refuse it instead of storing it.
CREATE TRIGGER `documents_workspace_required` BEFORE INSERT ON `documents` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'documents.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `documents_workspace_kept` BEFORE UPDATE OF `workspace_id` ON `documents` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'documents.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `chunks_workspace_required` BEFORE INSERT ON `chunks` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'chunks.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `chunks_workspace_kept` BEFORE UPDATE OF `workspace_id` ON `chunks` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'chunks.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `memories_workspace_required` BEFORE INSERT ON `memories` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'memories.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `memories_workspace_kept` BEFORE UPDATE OF `workspace_id` ON `memories` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'memories.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `jobs_workspace_required` BEFORE INSERT ON `jobs` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'jobs.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `jobs_workspace_kept` BEFORE UPDATE OF `workspace_id` ON `jobs` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'jobs.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `api_keys_workspace_required` BEFORE INSERT ON `api_keys` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'api_keys.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `api_keys_workspace_kept` BEFORE UPDATE OF `workspace_id` ON `api_keys` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'api_keys.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `profiles_workspace_required` BEFORE INSERT ON `profiles` WHEN NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'profiles.workspace_id is required'); END;
