# 0033 — A hidden Convia says who wrote, not what

## Status

Accepted

## Date

2026-10-16

## Context

Convia keeps running with its window closed ([ADR 0007](0007-closing-the-window-hides-convia.md)), so it has to tell the person something arrived. Whatever a notification shows, anybody near the screen can read.

## Decision

- **A message arriving while its conversation is not on screen raises a Windows notification and marks the tray icon.** The mark stays while anything is unread.
- **The notification names the sender and nothing else** — "New message from Ana". Showing the text is a setting, off by default.
- **Choosing the notification opens the conversation.**
- **Nothing is raised for a conversation open in a window the person is looking at.**

## Consequences

- The notification, the setting and the mark are the application's; Windows' own Focus Assist and notification settings still decide whether it appears.
