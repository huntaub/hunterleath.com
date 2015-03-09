package controllers

import "github.com/revel/revel"

type App struct {
	*revel.Controller
}

func (c App) Index() revel.Result {
	var posts []*Post
	if revel.Config.BoolDefault("blog.enabled", false) {
		posts = theEngine.List()

		// Trim it down!
		if len(posts) > 5 {
			posts = posts[:5]
		}
	}

	return c.Render(posts)
}

func (c App) About() revel.Result {
	return c.Render()
}
