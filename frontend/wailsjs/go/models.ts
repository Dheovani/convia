export namespace app {
	
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

