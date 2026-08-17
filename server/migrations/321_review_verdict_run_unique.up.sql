CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_review_verdict_run_unique
    ON review_verdict(review_run_id);
