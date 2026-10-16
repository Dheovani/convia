export namespace app {
	
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
	export class QuoteView {
	    id: string;
	    mine: boolean;
	    text: string;
	    missing: boolean;
	
	    static createFrom(source: any = {}) {
	        return new QuoteView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.mine = source["mine"];
	        this.text = source["text"];
	        this.missing = source["missing"];
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
	    text: string;
	    status: string;
	    at: number;
	    reactions: ReactionView[];
	    replyTo?: QuoteView;
	
	    static createFrom(source: any = {}) {
	        return new MessageView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.mine = source["mine"];
	        this.text = source["text"];
	        this.status = source["status"];
	        this.at = source["at"];
	        this.reactions = this.convertValues(source["reactions"], ReactionView);
	        this.replyTo = this.convertValues(source["replyTo"], QuoteView);
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

