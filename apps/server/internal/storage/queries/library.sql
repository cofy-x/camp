-- name: ListLibraryResources :many
SELECT * FROM resources ORDER BY updated_at DESC,id;

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
