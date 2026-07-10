package parse

import (
	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/rss"
)

// commentsCustomKey is the Custom-map key commentsTranslator stores the RSS
// <comments> URL under. The key is exclusively ours: the rss parser maps a
// real <comments> element (any case) onto rss.Item.Comments before its Custom
// fallback, and namespaced lookalikes (slash:comments) land in Extensions, so
// feed content can never occupy it.
const commentsCustomKey = "comments"

// commentsTranslator wraps DefaultRSSTranslator to carry each item's RSS
// <comments> URL across the universal-feed translation, which otherwise drops
// it (gofeed.Item has no Comments field and the default translator never maps
// rss.Item.Comments). Raw and translated items are paired BY INDEX, sound
// because translateFeedItems emits exactly one Item per rss.Feed.Items
// element, in order (gofeed v1.3.0); the length guard degrades any future
// divergence to no-capture rather than mis-pairing.
type commentsTranslator struct {
	inner *gofeed.DefaultRSSTranslator
}

func (t *commentsTranslator) Translate(feed interface{}) (*gofeed.Feed, error) {
	f, err := t.inner.Translate(feed)
	if err != nil {
		return nil, err
	}
	rf, ok := feed.(*rss.Feed) // inner already rejected non-RSS; defensive
	if !ok || len(rf.Items) != len(f.Items) {
		return f, nil
	}
	for i, ri := range rf.Items {
		if ri.Comments == "" {
			continue
		}
		it := f.Items[i]
		// The default translator ALIASES item.Custom to rss.Item.Custom (the
		// map the rss parser fills with unknown elements). Copy before writing
		// so we never mutate a map another struct owns.
		m := make(map[string]string, len(it.Custom)+1)
		for k, v := range it.Custom {
			m[k] = v
		}
		m[commentsCustomKey] = ri.Comments
		it.Custom = m
	}
	return f, nil
}
