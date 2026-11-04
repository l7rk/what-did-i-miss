# What Did I Miss?

A small Windows app that finds the parts of an OpenLearn course that haven't ticked yet.

It reads a copy of your **Course contents** page that you save yourself. It never logs in, never asks for your password, and never marks anything as done.

## Features

- Lists every page and quiz without a tick
- Opens week introductions and numbered sections, either one at a time or in batches of 5 with a pause in between
- Leaves quizzes for you to do yourself
- Skips further reading, references and similar pages
- Re-check: save the page again and it shows which items **got their tick** and which are **not yet**
- Light and dark mode
- Runs only on your computer and closes when you close its tab

## How to use

1. Open your course on OpenLearn and go to **Course contents**.
2. Click **Expand all content**.
3. Press **Ctrl + S** and save as **Webpage, Complete**.
4. Open `WhatDidIMiss.exe` and drop the saved `.html` file onto it.

Windows may show "Windows protected your PC" because the app isn't signed. Click **More info**, then **Run anyway**.

## Building

You need [Go](https://go.dev/dl/) 1.24 or newer.

```
go build -ldflags="-s -w -H windowsgui" -o WhatDidIMiss.exe .
```

GitHub also builds it automatically on every push: open the **Actions** tab, click the latest run, and download **WhatDidIMiss** from the Artifacts section.
