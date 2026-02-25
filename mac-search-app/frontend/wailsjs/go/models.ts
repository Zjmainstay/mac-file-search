export namespace main {
	
	export class FileEntry {
	    id: number;
	    path: string;
	    name: string;
	    size: number;
	    mod_time: number;
	    is_dir: boolean;
	    ext: string;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.mod_time = source["mod_time"];
	        this.is_dir = source["is_dir"];
	        this.ext = source["ext"];
	    }
	}
	export class SearchOptions {
	    keyword: string;
	    use_regex: boolean;
	    extensions: string[];
	    path_filter: string;
	    min_size: number;
	    max_size: number;
	
	    static createFrom(source: any = {}) {
	        return new SearchOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.keyword = source["keyword"];
	        this.use_regex = source["use_regex"];
	        this.extensions = source["extensions"];
	        this.path_filter = source["path_filter"];
	        this.min_size = source["min_size"];
	        this.max_size = source["max_size"];
	    }
	}

}

