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

