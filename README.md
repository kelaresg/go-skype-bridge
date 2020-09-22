# matrix-skype

matrix-skype is a library for bridging matrix and skype, about matrix, please refer to [matrix.org](http://matrix.org/).

## functions are available 
`The following functions are available in both directions without special instructions）`

* create private conversation
* create group conversation
* private conversation
* group conversation
* kick/invite/leave/join(group)
* generate invitation link(group)
* quote message(Circular references may have some bugs)
* mention someone(message)
* media message
* picture message
* group avatar/name change
* user name/avatar change
* Typing status

The skype api lib of matrix-skype is [go-skypeapi](https://github.com/kelaresg/go-skypeapi).  
Note: Use `go get github.com/kelaresg/go-skypeapi@{latest_commit_id}`, for now is: `go get github.com/kelaresg/go-skypeapi@2bd2763a9a9835774738009547301ebc37220c24`

This matrix-skype bridge is based on [mautrix-whatsapp](https://github.com/tulir/mautrix-whatsapp),so the installation and usage methods are very similar to mautrix-whatsapp(matrix-skype currently does not support docker installation)

> # mautrix-whatsapp
> A Matrix-WhatsApp puppeting bridge based on the [Rhymen/go-whatsapp](https://github.com/Rhymen/go-whatsapp)
> implementation of the [sigalor/whatsapp-web-reveng](https://github.com/sigalor/whatsapp-web-reveng) project.

> ### [Wiki](https://github.com/tulir/mautrix-whatsapp/wiki)
