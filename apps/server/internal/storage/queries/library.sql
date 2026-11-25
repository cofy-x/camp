-- name: ListLibraryResources :many
SELECT r.*,
 CAST(COALESCE(u.name,'') AS TEXT) AS upload_name,
 CAST(COALESCE(u.uploaded_by_user_id,'') AS TEXT) AS uploaded_by_user_id,
 CAST(COALESCE(u.size,0) AS INTEGER) AS upload_size,
 CAST(COALESCE(u.created_at,'') AS TEXT) AS uploaded_at,
 CAST(COALESCE(i.id,'') AS TEXT) AS import_id,
 CAST(COALESCE(i.runner_id,'') AS TEXT) AS import_runner_id,
 CAST(COALESCE(i.commit_id,'') AS TEXT) AS import_commit,
 CAST(COALESCE(i.source_alias,'') AS TEXT) AS import_alias,
 CAST(COALESCE(i.created_at,'') AS TEXT) AS import_created_at
FROM resources r
LEFT JOIN room_uploads u ON u.resource_id=r.id
LEFT JOIN source_imports i ON i.resource_id=r.id AND i.status='succeeded'
ORDER BY r.updated_at DESC,r.id;

-- name: CreateResourcePublication :exec
INSERT INTO resource_publications(id,resource_id,previous_commit,commit_id,author_user_id,run_id,kind,path,checks_status,request_id,request_hash,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?);

-- name: GetResourcePublication :one
SELECT * FROM resource_publications WHERE id=?;

-- name: GetPublicationByRequest :one
SELECT * FROM resource_publications WHERE resource_id=? AND author_user_id=? AND request_id=?;

-- name: ListResourcePublications :many
SELECT p.*,COALESCE(u.name,'') AS author_name FROM resource_publications p
LEFT JOIN users u ON u.id=p.author_user_id
WHERE p.resource_id=@resource_id AND (@before_seq=0 OR p.seq<@before_seq)
ORDER BY p.seq DESC LIMIT 100;

-- name: PublicationRevisionKnown :one
SELECT EXISTS(SELECT 1 FROM resource_publications WHERE resource_id=@resource_id AND (commit_id=@commit_id OR previous_commit=@commit_id));
