CREATE TABLE avatars (
     id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
     user_id VARCHAR(255) NOT NULL,
     file_name VARCHAR(255) NOT NULL,
     mime_type VARCHAR(100) NOT NULL,
     size_bytes BIGINT NOT NULL,
     s3_key VARCHAR(500),
     thumbnail_s3_keys JSONB,
     upload_status VARCHAR(50) DEFAULT 'uploading',
     processing_status VARCHAR(50) DEFAULT 'pending',
     created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
     updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
     deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_avatars_user_id ON avatars(user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_avatars_status ON avatars(upload_status, processing_status);