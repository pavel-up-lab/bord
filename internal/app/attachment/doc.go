// Package attachment implements the attachment application use cases.
//
// Integration boundary: Repository is implemented by the storage lane. Attach
// must atomically verify that the post exists and is eligible, reject an
// attachment already linked to any post, reject a post that already has an
// attachment, and transition the requested prepared row to attached. PublicByID
// must return a row only while its linked post is publicly visible. These rules
// intentionally live in this lane-local contract because foundation's frozen
// ports contain no attachment metadata repository.
package attachment
