package controllers

import (
	"fmt"
	"html/template"
	"sort"
	"time"

	"airdispat.ch/errors"
	"airdispat.ch/identity"
	"airdispat.ch/message"
	"airdispat.ch/routing"
	"airdispat.ch/server"
	"airdispat.ch/wire"
	"getmelange.com/router"
	"github.com/extemporalgenome/slug"
	"github.com/revel/revel"
	"github.com/russross/blackfriday"
)

type Blog struct {
	*revel.Controller
}

func (b *Blog) View(slug string) revel.Result {
	post := theEngine.Get(slug)
	if post == nil {
		return b.NotFound("The specified post was not found on the server.")
	}

	return b.Render(post)
}

func (b *Blog) List() revel.Result {
	posts := theEngine.List()

	return b.Render(posts)
}

type Post struct {
	Slug      string
	Title     string
	Body      template.HTML
	Author    string
	Name      string
	Published time.Time
}

var (
	theAddress = "hleath@airdispatch.me"
	theAuthor  = "Hunter Leath"
)

type blogEngine struct {
	Router *router.Router
	Cache  map[string]*Post

	cacheTimer      *time.Timer
	listRequest     chan chan []*Post
	specificRequest chan postRequest
}

type postRequest struct {
	Request string
	Reply   chan *Post
}

var theEngine *blogEngine

func (v *blogEngine) Get(name string) *Post {
	request := postRequest{
		Request: name,
		Reply:   make(chan *Post),
	}
	v.specificRequest <- request
	return <-request.Reply
}

func (v *blogEngine) List() []*Post {
	reply := make(chan []*Post)
	v.listRequest <- reply
	return <-reply
}

type Collection []*Post

func (c Collection) Len() int           { return len(c) }
func (c Collection) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c Collection) Less(i, j int) bool { return c[i].Published.After(c[j].Published) }

func (v *blogEngine) Start() {
	if err := v.getMessages(); err != nil {
		panic(err)
	}

	v.cacheTimer = time.NewTimer(1 * time.Minute)
	v.listRequest = make(chan chan []*Post)
	v.specificRequest = make(chan postRequest)

	go func() {
		for {
			select {
			case <-v.cacheTimer.C:
				revel.INFO.Printf("Downloading Messages\n")
				if err := v.getMessages(); err != nil {
					revel.ERROR.Printf("Error Downloading Messages: %s\n", err.Error())
				}
			case reply := <-v.listRequest:
				var out []*Post
				for _, v := range v.Cache {
					out = append(out, v)
				}

				// Sort those posts!
				sort.Sort(Collection(out))

				reply <- out
			case specific := <-v.specificRequest:
				specific.Reply <- v.Cache[specific.Request]
			}
		}
	}()
}

func (v *blogEngine) getMessages() error {
	srv, err := v.Router.LookupAlias(theAddress, routing.LookupTypeTX)
	if err != nil {
		return err
	}

	author, err := v.Router.LookupAlias(theAddress, routing.LookupTypeMAIL)
	if err != nil {
		return err
	}

	conn, err := message.ConnectToServer(srv.Location)
	if err != nil {
		return err
	}

	txMsg := server.CreateTransferMessageList(0, v.Router.Origin.Address, srv, author)

	err = message.SignAndSendToConnection(txMsg, v.Router.Origin, srv, conn)
	if err != nil {
		return err
	}

	recvd, err := message.ReadMessageFromConnection(conn)
	if err != nil {
		return err
	}

	byt, typ, h, err := recvd.Reconstruct(v.Router.Origin, true)
	if err != nil {
		return err
	}

	if typ == wire.ErrorCode {
		return errors.CreateErrorFromBytes(byt, h)
	} else if typ != wire.MessageListCode {
		return fmt.Errorf("Expected type (%s), got type (%s)", wire.MessageListCode, typ)
	}

	msgList, err := server.CreateMessageListFromBytes(byt, h)
	if err != nil {
		return err
	}

	for i := uint64(0); i < msgList.Length; i++ {
		newMsg, err := message.ReadMessageFromConnection(conn)
		if err != nil {
			continue
			// return err
		}

		byt, typ, h, err := newMsg.Reconstruct(v.Router.Origin, false)
		if err != nil {
			continue
			// return err
		}

		if typ == wire.ErrorCode {
			continue
			// return nil, errToHTTP(
			// 	errors.CreateErrorFromBytes(byt, h),
			// )
		} else if typ != wire.MailCode {
			continue
			// return nil, errToHTTP(
			// 	fmt.Errorf("Expected type (%s), got type (%s)", wire.MailCode, typ),
			// )
		}

		// Message is of the correct type.
		mail, err := message.CreateMailFromBytes(byt, h)
		if err != nil {
			continue
			// return err
		}

		if mail.Components.HasComponent("airdispat.ch/notes/title") {
			p := &Post{
				Title:     mail.Components.GetStringComponent("airdispat.ch/notes/title"),
				Body:      template.HTML(blackfriday.MarkdownCommon(mail.Components.GetComponent("airdispat.ch/notes/body"))),
				Author:    theAuthor,
				Name:      mail.Name,
				Published: time.Unix(int64(h.Timestamp), 0),
			}
			p.Slug = slug.Slug(p.Title)

			v.Cache[p.Slug] = p
		}
	}

	return nil
}

func initBlogEngine() {
	revel.INFO.Printf("Starting blogEngine\n")

	id, err := identity.CreateIdentity()
	if err != nil {
		panic(err)
	}

	theEngine = &blogEngine{
		Router: &router.Router{
			Origin: id,
			TrackerList: []string{
				"airdispatch.me",
			},
			Redirects: 10,
		},
		Cache: make(map[string]*Post),
	}
	theEngine.Start()
}

func init() {
	revel.TemplateFuncs["format"] = func(t time.Time, f string) string { return t.Format(f) }
	revel.OnAppStart(initBlogEngine)
}
