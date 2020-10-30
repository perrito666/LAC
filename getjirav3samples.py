import urllib.request
import urllib.parse
import json
import os
from os.path import join as pjoin
from pprint import pprint, pformat
from html.parser import HTMLParser


API_V3_URL="https://developer.atlassian.com/cloud/jira/platform/rest/v3/"
DATA_PREFIX="window.__DATA__ = "
JIRA_FOLDER="jira"
T_DICT = type({})
T_LIST = type([])

def main():
    f = urllib.request.urlopen(API_V3_URL)
    src = f.read().decode('utf-8')
    if not os.path.isdir(JIRA_FOLDER):
        os.mkdir(JIRA_FOLDER)
    extract_examples_json(src)
    
def extract_examples_json(src=""):
    if os.path.isfile("json.log"):
        with open("json.log", mode="r")  as f:
            loaded_json = f.read()
        j= json.loads(loaded_json)
    else:
        p = JIRAAPIDocHTMLParser()
        p.feed(src)
        j = p.get_json()
    for a_path, v in j["schema"]["paths"].items():
        for a_method in v.keys():
            print(a_path)
            print(a_method)
            for a_response in v[a_method]["responses"].keys():
                if a_response in ["200", "201"]:
                    print(a_response)
                    if "content" not in v[a_method]["responses"][a_response].keys():
                        continue
                    app_json = v[a_method]["responses"][a_response]["content"]["application/json"]
                    schema = app_json["schema"]
                    if "$ref" not in app_json["schema"].keys():
                        continue
                    if "type" in schema.keys() and  schema["type"] == "array":
                        schema_path = app_json["schema"]["items"]["$ref"]
                    else:
                        schema_path = app_json["schema"]["$ref"]
                    type_name = schema_path.split("/")[-1]
                    if "example" not in app_json.keys():
                        continue
                    try:
                        ex_str = app_json["example"]
                        if type(ex_str) in [T_DICT, T_LIST]:
                            example = json.dumps(ex_str)
                        else:
                            example = json.dumps(json.loads(ex_str))
                    except json.JSONDecodeError:
                        print("COULD NOT PARSE: {}".format(app_json["example"]))
                        continue
                    # perhaps append here if the file exists?
                    with open(pjoin(JIRA_FOLDER, type_name+".json") , mode="wb") as fp:
                        fp.write(example.encode('utf-8'))




class JIRAAPIDocHTMLParser(HTMLParser):
    def __init__(self, *args, **kwargs):
        super(JIRAAPIDocHTMLParser, self).__init__(*args, **kwargs)
        self.__in_head = False
        self.__found_script = False
        self.__collected_json = ""

    def handle_starttag(self, tag, attrs):
        if self.__in_head and not self.__found_script and tag == "script":
            self.__found_script = True
            return
        if not self.__in_head and tag == "head":
            self.__in_head = True

    def handle_data(self, data):
        if data.startswith(DATA_PREFIX):
            self.__collected_json = data[len(DATA_PREFIX)-1:-1] #ends with colon
        
    def get_json(self):
        if self.__collected_json == "":
            return
        with open("json.log", mode="w")  as f:
            f.write(self.__collected_json)
        return json.loads(self.__collected_json)
        

if __name__ == "__main__":
    main()