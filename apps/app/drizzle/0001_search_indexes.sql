-- Search indexes. drizzle-kit can't describe virtual tables, so they live here.
-- Only current rows are indexed: superseded and deleted memories are removed from
-- memories_fts and memories_vec, so search never has to filter on valid_to.
CREATE VIRTUAL TABLE `chunks_fts` USING fts5(
	text,
	chunk_id UNINDEXED,
	container UNINDEXED,
	tokenize = 'porter unicode61 remove_diacritics 2'
);
--> statement-breakpoint
CREATE VIRTUAL TABLE `memories_fts` USING fts5(
	fact,
	keywords,
	memory_id UNINDEXED,
	container UNINDEXED,
	tokenize = 'porter unicode61 remove_diacritics 2'
);
--> statement-breakpoint
CREATE VIRTUAL TABLE `chunks_vec` USING vec0(
	chunk_id text primary key,
	container text partition key,
	embedding float[384] distance_metric=cosine
);
--> statement-breakpoint
CREATE VIRTUAL TABLE `memories_vec` USING vec0(
	memory_id text primary key,
	container text partition key,
	embedding float[384] distance_metric=cosine
);
