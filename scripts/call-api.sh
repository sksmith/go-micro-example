curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"sku":"sku123","upc":"someupc","name":"some product"}' \
    http://localhost:8080/api/inventory/v1

curl -i -H "content-type:application/json" -u admin:admin \
    http://localhost:8080/api/inventory/v1

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"produceReq1","quantity":5}' \
    http://localhost:8080/api/inventory/v1/sku123/productionEvent

curl -i -H "content-type:application/json" -u admin:admin \
    http://localhost:8080/api/inventory/v1/sku123

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq1","requester":"someperson","quantity":2}' \
    http://localhost:8080/api/inventory/v1/sku123/reservation

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq2","requester":"someperson","quantity":2}' \
    http://localhost:8080/api/inventory/v1/sku123/reservation

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq3","requester":"someperson","quantity":2}' \
    http://localhost:8080/api/inventory/v1/sku123/reservation

curl -i -H "content-type:application/json" -u admin:admin \
    -XPUT -d'{"requestID":"reserveReq4","requester":"someperson","quantity":2}' \
    http://localhost:8080/api/inventory/v1/sku123/reservation

curl -i -H "content-type:application/json" -u admin:admin \
    http://localhost:8080/api/inventory/v1/sku123/reservation

curl -i -H "content-type:application/json" -u admin:admin \
    http://localhost:8080/api/inventory/v1/sku123/reservation?state=Open

curl -i -H "content-type:application/json" -u admin:admin \
    http://localhost:8080/api/inventory/v1/sku123/reservation?state=Closed

curl -i -H "content-type:application/json" -u admin:admin \
    http://localhost:8080/api/inventory/v1/sku123/reservation?state=InvalidState