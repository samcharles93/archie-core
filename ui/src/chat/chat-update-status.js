// updateUnavailableMessage decides what -- if anything -- refreshUpdate should
// show when fetching /api/chat/update fails.
//
// A 501 means the release-update-check feature simply isn't configured for
// this deployment (chat.telegram.update_check_command is unset) -- a normal
// state, the same category as an unconfigured chat channel, not a fetch
// failure. It gets no visible banner: callers should render nothing.
//
// Any other error (network failure, 500, ...) is a genuine problem and gets a
// clear, fixed human message -- never the raw fetch error text, which leaks
// implementation detail like literal HTTP status codes into the UI.
export function updateUnavailableMessage(err) {
  if (err?.status === 501) return null;
  return "Could not check for updates.";
}
