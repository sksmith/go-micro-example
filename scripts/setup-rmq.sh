curl -i -u guest:guest -H "content-type:application/json" \
    -XPUT -d'{"type":"direct","durable":true}' \
    http://localhost:15672/api/exchanges/%2F/inventory.exchange

curl -i -u guest:guest -H "content-type:application/json" \
    -XPUT -d'{"auto_delete":false,"durable":true}' \
    http://localhost:15672/api/queues/%2F/inventory.queue

curl -i -u guest:guest -H "content-type:application/json" \
    -XPOST -d'{}' \
    http://localhost:15672/api/bindings/%2F/e/inventory.exchange/q/inventory.queue




curl -i -u guest:guest -H "content-type:application/json" \
    -XPUT -d'{"type":"direct","durable":true}' \
    http://localhost:15672/api/exchanges/%2F/reservation.exchange

curl -i -u guest:guest -H "content-type:application/json" \
    -XPUT -d'{"auto_delete":false,"durable":true}' \
    http://localhost:15672/api/queues/%2F/reservation.queue

curl -i -u guest:guest -H "content-type:application/json" \
    -XPOST -d'{}' \
    http://localhost:15672/api/bindings/%2F/e/reservation.exchange/q/reservation.queue




curl -i -u guest:guest -H "content-type:application/json" \
    -XPUT -d'{"type":"direct","durable":true}' \
    http://localhost:15672/api/exchanges/%2F/product.exchange

curl -i -u guest:guest -H "content-type:application/json" \
    -XPUT -d'{"auto_delete":false,"durable":true}' \
    http://localhost:15672/api/queues/%2F/product.queue

curl -i -u guest:guest -H "content-type:application/json" \
    -XPOST -d'{}' \
    http://localhost:15672/api/bindings/%2F/e/product.exchange/q/product.queue

