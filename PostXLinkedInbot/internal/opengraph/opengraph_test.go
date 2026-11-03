package opengraph

import (
	"testing"
)

func TestExtractArticleImageFallback(t *testing.T) {
	htmlSample := `<!DOCTYPE html>
<html>
<head>
<title>Tech Firms Dismantle Botnet That Turned 1.3M TV Boxes Into Proxies &#8211; Krebs on Security</title>
<meta name='robots' content='max-image-preview:large' />
</head>
<body>
<div id="header">
	<img src="/b-flashpoint/5.png">
	<img src="https://krebsonsecurity.com/wp-content/uploads/2021/03/kos-27-03-2021.jpg" alt="Krebs on Security">
</div>
<article id="post-74548" class="post-74548 post type-post">
	<div class="entry-content">
		<p>Google said today it worked with industry partners to dismantle a botnet...</p>
		<img class="wp-smiley" src="https://krebsonsecurity.com/emoji.png" width="16" height="16" />
		<div id="attachment_74560" class="wp-caption aligncenter">
			<img fetchpriority="high" decoding="async" class=" wp-image-74560" src="https://krebsonsecurity.com/wp-content/uploads/2026/11/xcpe-map.png" alt="" width="749" height="418" />
		</div>
	</div>
</article>
</body>
</html>`

	meta := Extract([]byte(htmlSample), "https://krebsonsecurity.com/2026/11/tech-firms-dismantle-botnet-that-turned-1-3m-tv-boxes-into-proxies/")
	wantImg := "https://krebsonsecurity.com/wp-content/uploads/2026/11/xcpe-map.png"
	if meta.ImageURL != wantImg {
		t.Fatalf("Extract ImageURL = %q, want %q", meta.ImageURL, wantImg)
	}
	wantTitle := "Tech Firms Dismantle Botnet That Turned 1.3M TV Boxes Into Proxies – Krebs on Security"
	if meta.Title != wantTitle {
		t.Fatalf("Extract Title = %q, want %q", meta.Title, wantTitle)
	}
}
