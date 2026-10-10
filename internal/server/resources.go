package server

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/fileview"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// A file attached to a post is also an MCP resource (ADR-029): it has a fixed
// content and a type, and as a resource it has an address a client can hold
// on to and read when it needs it, rather than when a tool answers. The
// address is mattermost://<server>/files/<id>, not the file's https address,
// which a client would try to fetch itself, without the credential. Reading
// it converts the file as read_file does. read_file and save_file stay: in
// many clients only the person can open a resource, and a resource writes
// nothing to disk.

// ResourceSpec is a resource template mm-mcp offers, with the operations
// reading a resource and completing the template's argument call, accounted
// for as a tool's are (ADR-028).
type ResourceSpec struct {
	// Name is the template's name.
	Name string
	// Template is the template a server for the Mattermost at an address lists.
	Template func(address string) *mcp.ResourceTemplate
	// Read reads one resource of the template.
	Read func(clientFor ClientFor, address string) mcp.ResourceHandler
	// Uses is every operation reading a resource calls.
	Uses []Use
	// Complete suggests values of the template's argument from what the
	// person typed.
	Complete func(ctx context.Context, client *mattermost.Client, argument, typed string) (mcp.CompletionResultDetails, error)
	// CompletionUses is every operation completing an argument calls.
	CompletionUses []Use
}

// AllResources is every resource template mm-mcp offers, whatever the
// configuration: each only reads.
func AllResources() []ResourceSpec {
	return []ResourceSpec{fileResourceSpec()}
}

// fileResource is the address of a file of the server at server as a
// resource.
func fileResource(server *url.URL, id string) string {
	return fileResourceScheme + "://" + strings.ToLower(server.Host) + strings.TrimRight(server.Path, "/") + "/files/" + id
}

// fileResourceID is the file a resource address names on the server at
// server.
func fileResourceID(server, address *url.URL) (string, bool) {
	if !strings.EqualFold(address.Scheme, fileResourceScheme) || !strings.EqualFold(address.Host, server.Host) {
		return "", false
	}
	id, ok := strings.CutPrefix(address.Path, strings.TrimRight(server.Path, "/")+"/files/")
	return id, ok && mattermostID.MatchString(id)
}

// fileURI is a file's resource address on the client's server.
func fileURI(client *mattermost.Client, id string) string {
	server, err := url.Parse(client.Address())
	if err != nil || id == "" {
		return ""
	}
	return fileResource(server, id)
}

// fileArgument is the file template's one argument, named as tools name it.
const fileArgument = "file_id"

func fileResourceSpec() ResourceSpec {
	return ResourceSpec{
		Name: "file",
		Template: func(address string) *mcp.ResourceTemplate {
			server, err := url.Parse(strings.TrimRight(address, "/"))
			if err != nil {
				panic(fmt.Sprintf("the server's address %q: %v", address, err))
			}
			return &mcp.ResourceTemplate{
				Name:        "file",
				Title:       "Mattermost file",
				URITemplate: fileResource(server, "{"+fileArgument+"}"),
				Description: "A file attached to a post, converted as read_file converts it: text, the text of a Word, PowerPoint or Excel " +
					"file, and an archive's listing as text; an image as an image, scaled as read_file scales it; a PDF, audio, video " +
					fmt.Sprintf("or any other file as itself. A resource is read whole, up to %s; read_file reads a longer text in windows.", sizeOf(fileview.MediaBytes)),
			}
		},
		Read: readFileResource,
		Uses: fileOperationUses(),
		Complete: func(ctx context.Context, client *mattermost.Client, argument, typed string) (mcp.CompletionResultDetails, error) {
			none := mcp.CompletionResultDetails{Values: []string{}}
			typed = strings.TrimSpace(typed)
			if argument != fileArgument || typed == "" || isLink(typed) || mattermostID.MatchString(typed) {
				return none, nil
			}
			found, err := client.SearchFiles(ctx, mattermost.FileSearch{Terms: typed + "*", PerPage: maxCompletions})
			if err != nil {
				return none, err
			}
			var files []*model.FileInfo
			for _, id := range found.Order {
				if file := found.FileInfos[id]; file != nil {
					files = append(files, file)
				}
			}
			slices.SortStableFunc(files, func(a, b *model.FileInfo) int {
				if a.CreateAt != b.CreateAt {
					return int(b.CreateAt - a.CreateAt)
				}
				return strings.Compare(a.Id, b.Id)
			})
			values := make([]string, 0, len(files))
			for _, file := range files {
				values = append(values, file.Id)
			}
			// Mattermost says nothing of how many more match; a full page may
			// have more behind it.
			return mcp.CompletionResultDetails{Values: values, HasMore: len(values) >= maxCompletions}, nil
		},
		CompletionUses: []Use{{
			Operation: "SearchFiles",
			Params: map[string]Coverage{
				"team_id":                       Omitted("a file is suggested from every team the person can read"),
				"body.terms":                    SetBy(fileArgument),
				"body.is_or_search":             Omitted("each word typed narrows the files, as in Mattermost's search box"),
				"body.page":                     Fixed("0", "a suggestion is the first, newest files that match"),
				"body.per_page":                 Fixed(fmt.Sprint(maxCompletions), "the most values one completion holds"),
				"body.time_zone_offset":         Omitted("nothing typed is a day"),
				"body.include_deleted_channels": Omitted("archived channels are left out of a search, as they are out of search_files"),
			},
		}},
	}
}

// maxCompletions is the most values MCP lets one completion hold.
const maxCompletions = 100

// readFileResource reads a file's resource: the file, converted as read_file
// converts it, whole.
func readFileResource(clientFor ClientFor, address string) mcp.ResourceHandler {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		server, err := url.Parse(strings.TrimRight(address, "/"))
		if err != nil {
			return nil, err
		}
		given, err := url.Parse(uri)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		id, ok := fileResourceID(server, given)
		if !ok {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		client, err := clientFor(ctx, request)
		if err != nil {
			return nil, err
		}
		info, err := client.FileInfo(ctx, id)
		if notFound(err) {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		if err != nil {
			return nil, err
		}
		contents, err := wholeFile(ctx, client, info)
		if err != nil {
			return nil, err
		}
		contents.URI = uri
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{contents}}, nil
	}
}

// plainText is the type of text extracted from a file, or listed from it.
const plainText = "text/plain; charset=utf-8"

// wholeFile is a file as a resource: converted as read_file converts it, and
// returned whole, which a resource is read, up to the most read_file returns
// in one piece of content. A file past that, or past what read_file reads at
// all, is refused with what reads it instead.
func wholeFile(ctx context.Context, client *mattermost.Client, info *model.FileInfo) (*mcp.ResourceContents, error) {
	opens := client.Permalink(info.PostId)
	if info.Size > fileview.MaxFileBytes {
		return nil, fmt.Errorf("%s is %s, more than the %s a file is read up to; save_file saves it, and the person can open it from its post: %s",
			info.Name, sizeOf(info.Size), sizeOf(fileview.MaxFileBytes), opens)
	}
	data, err := client.File(ctx, info.Id)
	if err != nil {
		return nil, err
	}
	view, err := fileview.Read(ctx, fileview.Request{Name: info.Name, WebURL: opens}, data)
	if err != nil {
		return nil, err
	}
	tooLarge := func(what string, size int) error {
		return fmt.Errorf("%s is %s, more than the %s a resource holds; %s, and the person can open it from its post: %s",
			what, sizeOf(int64(size)), sizeOf(fileview.MediaBytes), "read_file reads a text in windows, and save_file saves a file", opens)
	}
	isPDF := view.MIMEType == pdfType || info.MimeType == pdfType
	switch {
	case isPDF && len(data) <= fileview.MediaBytes:
		// A PDF is the client's to convert, as read_file leaves it.
		return &mcp.ResourceContents{MIMEType: pdfType, Blob: data}, nil
	case view.Window != nil:
		if len(view.Whole) > fileview.MediaBytes {
			return nil, tooLarge("the text of "+info.Name, len(view.Whole))
		}
		mimeType := plainText
		if view.Kind == fileview.KindText && view.MIMEType != "" {
			mimeType = view.MIMEType
		}
		if view.Whole == "" {
			// Text resource contents must hold text, and an empty one would be
			// left out of the answer altogether.
			return &mcp.ResourceContents{MIMEType: mimeType, Blob: []byte{}}, nil
		}
		return &mcp.ResourceContents{MIMEType: mimeType, Text: view.Whole}, nil
	case view.Image != nil:
		return &mcp.ResourceContents{MIMEType: view.Image.MIMEType, Blob: view.Image.Data}, nil
	case view.Media != nil:
		return &mcp.ResourceContents{MIMEType: view.Media.MIMEType, Blob: view.Media.Data}, nil
	case len(data) <= fileview.MediaBytes:
		mimeType := cmp.Or(view.MIMEType, info.MimeType, "application/octet-stream")
		return &mcp.ResourceContents{MIMEType: mimeType, Blob: data}, nil
	}
	return nil, tooLarge(info.Name, len(data))
}

// sizeOf is a size in bytes as a person reads it.
func sizeOf(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(size)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", size)
}

// completion is the server's answer to a request to complete an argument:
// the template the request names suggests values; any other reference has
// none.
func completion(clientFor ClientFor, address string, resources []ResourceSpec) func(context.Context, *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return func(ctx context.Context, request *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
		none := &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: []string{}}}
		ref := request.Params.Ref
		if ref == nil || ref.Type != "ref/resource" {
			return none, nil
		}
		for _, resource := range resources {
			if resource.Complete == nil || resource.Template(address).URITemplate != ref.URI {
				continue
			}
			client, err := clientFor(ctx, request)
			if err != nil {
				return nil, err
			}
			details, err := resource.Complete(ctx, client, request.Params.Argument.Name, request.Params.Argument.Value)
			if err != nil {
				return nil, err
			}
			return &mcp.CompleteResult{Completion: details}, nil
		}
		return none, nil
	}
}

// registerResources adds every resource template to a server.
func registerResources(server *mcp.Server, clientFor ClientFor, cfg config.Config, resources []ResourceSpec) {
	for _, resource := range resources {
		server.AddResourceTemplate(resource.Template(cfg.URL), resource.Read(clientFor, cfg.URL))
	}
}

// fileLinks are resource links to the files an answer names, each once, in
// the order the answer names them, so a client can read one when it needs it.
func fileLinks(answer reflect.Value) []mcp.Content {
	var links []mcp.Content
	seen := map[string]bool{}
	var walk func(value reflect.Value)
	walk = func(value reflect.Value) {
		switch value.Kind() {
		case reflect.Pointer:
			if !value.IsNil() {
				walk(value.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := range value.Len() {
				walk(value.Index(i))
			}
		case reflect.Struct:
			if value.Type() == reflect.TypeFor[Attachment]() {
				if file, ok := value.Interface().(Attachment); ok && file.URI != "" && !seen[file.URI] {
					seen[file.URI] = true
					links = append(links, fileLink(file))
				}
				return
			}
			for i := range value.NumField() {
				if field := value.Type().Field(i); field.IsExported() || field.Anonymous {
					walk(value.Field(i))
				}
			}
		}
	}
	walk(answer)
	return links
}

// fileLink is a resource link to an attached file.
func fileLink(file Attachment) *mcp.ResourceLink {
	link := &mcp.ResourceLink{URI: file.URI, Name: cmp.Or(file.Name, file.ID), MIMEType: file.MIMEType}
	if file.Size > 0 {
		size := file.Size
		link.Size = &size
	}
	return link
}
