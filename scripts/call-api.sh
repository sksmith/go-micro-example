curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"sku":"sku123","upc":"someupc","name":"some product"}' \
    "http://localhost:8080/api/v1/inventory"

curl -i -H "content-type:application/json" -u admin:admin \
    "http://localhost:8080/api/v1/inventory"

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"produceReq1","quantity":5}' \
    "http://localhost:8080/api/v1/inventory/sku123/productionEvent"

curl -i -H "content-type:application/json" -u admin:admin \
    "http://localhost:8080/api/v1/inventory/sku123"

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq1","sku":"sku123","requester":"someperson","quantity":2}' \
    "http://localhost:8080/api/v1/reservation"

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq2","requester":"someperson","quantity":2}' \
    "http://localhost:8080/api/v1/reservation"

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq3","sku":"sku123","requester":"someperson","quantity":2}' \
    "http://localhost:8080/api/v1/reservation"

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq4","sku":"sku123","requester":"someperson","quantity":2}' \
    "http://localhost:8080/api/v1/reservation"

curl -i -H "content-type:application/json" -u admin:admin \
    "http://localhost:8080/api/v1/reservation?sku=sku123"

curl -i -H "content-type:application/json" -u admin:admin \
    "http://localhost:8080/api/v1/reservation?sku=sku123&state=Open"

curl -i -H "content-type:application/json" -u admin:admin \
    "http://localhost:8080/api/v1/reservation?sku=sku123&state=Closed"

curl -i -H "content-type:application/json" -u admin:admin \
    "http://localhost:8080/api/v1/reservation?sku=sku123&state=InvalidState"


curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq14","requester":"Sean Smith", "sku": "powerbat1", "quantity":4}' \
    "http://localhost:8080/api/v1/reservation"

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq16","requester":"Sean Smith", "sku": "sku123", "quantity":3}' \
    "http://localhost:8080/api/v1/reservation"