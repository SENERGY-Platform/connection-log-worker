Receives log changes as amqp events and saves them. 
The current state will saved to a mongodb. 
The history will be saved to a influxdb.
A HTTP-API to request the history and current state is provided by the connection-log service.

In the SEPL-Platform the log-events will be published by the platform-connector service.