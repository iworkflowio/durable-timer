default: gen-all 

gen-all: api-code-gen-server # api-code-gen-ts api-code-gen-py

api-code-gen-server: #generate/refresh go server code for api.yaml, do this after update the api.yaml
	rm -Rf ./server/genapi ; true
	java -jar openapi-generator-cli-7.14.0.jar generate -i api.yaml -g go-gin-server -o server/genapi/ -p packageName=genapi -p generateInterfaces=true -p isGoSubmodule=false --git-user-id iworkflowio --git-repo-id durable-timer
	gofmt -s -w server/genapi;
	rm ./server/genapi/main.go ; rm ./server/genapi/go/routers.go ;  rm ./server/genapi/go.*; rm -rf ./server/genapi/api; rm -rf ./server/genapi/Dockerfile
	true

#api-code-gen-ts: #generate/refresh typescript apis
#	rm -Rf ./ts-api/src/api-gen ; true
#	java -jar openapi-generator-cli-7.14.0.jar generate -i api.yaml -g typescript-axios -o ./ts-api/src/api-gen --git-user-id xcherryio --git-repo-id apis

#api-code-gen-py: #generate/refresh python apis
#	rm -Rf ./pyapi/* ; true
#	java -jar openapi-generator-cli-7.14.0.jar generate -i api.yaml -g python -o ./pyapi -p packageVersion=0.0.3 -p packageName=xcherryapi --git-user-id xcherryio --git-repo-id apis

