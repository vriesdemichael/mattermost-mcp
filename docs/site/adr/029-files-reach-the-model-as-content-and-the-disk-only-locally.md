---
search:
  boost: 0.3
---

# ADR-029: Files reach the model as content, and the disk only from a local server

An attachment the model needs arrives as content every MCP client shows it: text, or an image, converted on the server by `internal/fileview`. Text comes back in windows of numbered lines the model pages through; a Word, PowerPoint or Excel file as its extracted text; an archive as a listing; an image as an image, turned upright and scaled to what clients take, with a note when it was scaled; small audio and video as themselves beside a description. Anything else is described by its type and size, with the post's link for the person to open, and a file over the cap is described without being read. A result never points the model at a command, a path or a download in place of content.

The machine's disk is reached only by a server that runs on it, which is a server serving over stdio: the client started it as a process on the person's own machine. Only such a server offers `save_file`, which writes an attachment into the download directory, `MM_MCP_DOWNLOAD_DIR` or the person's Downloads directory, under the attachment's name, never over an existing file, and answers with the path it wrote. A tool that writes this machine's files is marked `local` in its spec, reads Mattermost and never changes it, and so is offered whether Mattermost writes are allowed or not; a governance test holds it to operations that only read. Likewise `create_post`, `dm` and `group_message` attach a file from a path only on a local server. A path is refused before anything is opened when it names another machine or a device, as `\\host\share` does on Windows, where merely looking makes the machine sign in to the host with the person's credentials, or when it lies in a tree the system makes up, such as `/proc`, which holds this server's own credential. A link is followed and the file it points at is what the question names; a name with control or invisible characters is refused, and a file whose bytes disagree with its name's type says so. Over HTTP the server may run anywhere, and a path names a file on the server's machine, not the person's.

An upload is part of the post it belongs to. A tool that posts takes the files with the message, from a path on a local server or as text the model wrote, and the question names every file with its path, size and type before anything is sent. The files are uploaded only once the person accepts, so a declined post leaves no orphaned file in Mattermost, and an answer accepts the files' bytes as they were when the person was asked: a file changed since is refused, and the bytes the tool checks just before it uploads are the ones it uploads.

A model in a chat client can use only what the client shows it, so the server, which holds the bytes, converts them. A path on the person's machine is useful only when the person is at that machine, and the server can only know that when it runs there. A file read from disk and sent to colleagues is the one way a message in Mattermost could talk a model into leaking a local file, which is why the question names the path.

## Not chosen

- **Return a file's bytes as text or base64**: A binary file comes back corrupted or unreadable, and a large one fills the context in one piece.
- **Save to disk over HTTP**: The server's disk is not the person's, and on a shared deployment it is no one's to write.
- **A separate upload tool that returns file ids**: An upload nobody posts is an orphaned file, and the person would confirm the post without seeing the files it carries.
- **Let the model choose the path save_file writes to**: A model steered by a message could overwrite a file anywhere the person can write; one directory, never overwriting, bounds what it can touch.
- **Base64 file content from the model**: Models write base64 poorly and slowly; a file the model writes is text, and any other file comes from disk.
