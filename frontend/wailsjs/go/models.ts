export namespace app {
	
	export class CallView {
	    id: string;
	    publicKey: string;
	    incoming: boolean;
	    video: boolean;
	    state: string;
	    outcome?: string;
	    since: number;
	
	    static createFrom(source: any = {}) {
	        return new CallView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.publicKey = source["publicKey"];
	        this.incoming = source["incoming"];
	        this.video = source["video"];
	        this.state = source["state"];
	        this.outcome = source["outcome"];
	        this.since = source["since"];
	    }
	}
	export class ChannelView {
	    id: string;
	    name: string;
	    restricted: boolean;
	    roles: string[];
	    guarded: boolean;
	    writers: string[];
	    writes: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChannelView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.restricted = source["restricted"];
	        this.roles = source["roles"];
	        this.guarded = source["guarded"];
	        this.writers = source["writers"];
	        this.writes = source["writes"];
	    }
	}
	export class ContactView {
	    publicKey: string;
	    name: string;
	    verified: boolean;
	    safetyCode: string;
	
	    static createFrom(source: any = {}) {
	        return new ContactView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.publicKey = source["publicKey"];
	        this.name = source["name"];
	        this.verified = source["verified"];
	        this.safetyCode = source["safetyCode"];
	    }
	}
	export class FileView {
	    size: number;
	    type: string;
	    held: number;
	    complete: boolean;
	    picture: boolean;
	    orientation: number;
	    url?: string;
	    wanted?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.size = source["size"];
	        this.type = source["type"];
	        this.held = source["held"];
	        this.complete = source["complete"];
	        this.picture = source["picture"];
	        this.orientation = source["orientation"];
	        this.url = source["url"];
	        this.wanted = source["wanted"];
	    }
	}
	export class QuoteView {
	    id: string;
	    mine: boolean;
	    author?: string;
	    text: string;
	    missing: boolean;
	    deleted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new QuoteView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.mine = source["mine"];
	        this.author = source["author"];
	        this.text = source["text"];
	        this.missing = source["missing"];
	        this.deleted = source["deleted"];
	    }
	}
	export class ReactionView {
	    emoji: string;
	    count: number;
	    mine: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ReactionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.emoji = source["emoji"];
	        this.count = source["count"];
	        this.mine = source["mine"];
	    }
	}
	export class MessageView {
	    id: string;
	    mine: boolean;
	    author?: string;
	    text: string;
	    status: string;
	    at: number;
	    reactions: ReactionView[];
	    replyTo?: QuoteView;
	    edited: boolean;
	    deleted: boolean;
	    file?: FileView;
	    deletable?: boolean;
	    moderated?: boolean;
	    missed?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MessageView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.mine = source["mine"];
	        this.author = source["author"];
	        this.text = source["text"];
	        this.status = source["status"];
	        this.at = source["at"];
	        this.reactions = this.convertValues(source["reactions"], ReactionView);
	        this.replyTo = this.convertValues(source["replyTo"], QuoteView);
	        this.edited = source["edited"];
	        this.deleted = source["deleted"];
	        this.file = this.convertValues(source["file"], FileView);
	        this.deletable = source["deletable"];
	        this.moderated = source["moderated"];
	        this.missed = source["missed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConversationView {
	    publicKey: string;
	    name: string;
	    verified: boolean;
	    safetyCode: string;
	    last?: MessageView;
	    unread: number;
	
	    static createFrom(source: any = {}) {
	        return new ConversationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.publicKey = source["publicKey"];
	        this.name = source["name"];
	        this.verified = source["verified"];
	        this.safetyCode = source["safetyCode"];
	        this.last = this.convertValues(source["last"], MessageView);
	        this.unread = source["unread"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class FoundView {
	    publicKey: string;
	    name: string;
	    message: MessageView;
	
	    static createFrom(source: any = {}) {
	        return new FoundView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.publicKey = source["publicKey"];
	        this.name = source["name"];
	        this.message = this.convertValues(source["message"], MessageView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InvitationView {
	    id: string;
	    name: string;
	    inviter: string;
	
	    static createFrom(source: any = {}) {
	        return new InvitationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.inviter = source["inviter"];
	    }
	}
	export class MeView {
	    name: string;
	    publicKey: string;
	
	    static createFrom(source: any = {}) {
	        return new MeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.publicKey = source["publicKey"];
	    }
	}
	export class MemberView {
	    publicKey: string;
	    name: string;
	    rank: string;
	    roles: string[];
	    me: boolean;
	    muted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MemberView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.publicKey = source["publicKey"];
	        this.name = source["name"];
	        this.rank = source["rank"];
	        this.roles = source["roles"];
	        this.me = source["me"];
	        this.muted = source["muted"];
	    }
	}
	
	
	
	export class RequestView {
	    publicKey: string;
	    name: string;
	    safetyCode: string;
	    at: number;
	    expired: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RequestView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.publicKey = source["publicKey"];
	        this.name = source["name"];
	        this.safetyCode = source["safetyCode"];
	        this.at = source["at"];
	        this.expired = source["expired"];
	    }
	}
	export class RequestsView {
	    received: RequestView[];
	    sent: RequestView[];
	
	    static createFrom(source: any = {}) {
	        return new RequestsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.received = this.convertValues(source["received"], RequestView);
	        this.sent = this.convertValues(source["sent"], RequestView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RoleView {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new RoleView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class SpaceView {
	    id: string;
	    name: string;
	    rank: string;
	    muted: boolean;
	    channels: ChannelView[];
	
	    static createFrom(source: any = {}) {
	        return new SpaceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.rank = source["rank"];
	        this.muted = source["muted"];
	        this.channels = this.convertValues(source["channels"], ChannelView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace peers {
	
	export class Candidate {
	    candidate: string;
	    sdpMid: string;
	    sdpMLineIndex: number;
	    usernameFragment: string;
	
	    static createFrom(source: any = {}) {
	        return new Candidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.candidate = source["candidate"];
	        this.sdpMid = source["sdpMid"];
	        this.sdpMLineIndex = source["sdpMLineIndex"];
	        this.usernameFragment = source["usernameFragment"];
	    }
	}

}

