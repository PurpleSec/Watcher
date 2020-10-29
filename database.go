// Copyright (C) 2020 iDigitalFlame
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//

package watcher

var cleanStatements = []string{
	`DROP TABLES IF EXISTS Subscribers`,
	`DROP TABLES IF EXISTS Mappings`,
	`DROP PROCEDURE IF EXISTS UpdateMapping`,
	`DROP PROCEDURE IF EXISTS CleanupRoutine`,
	`DROP PROCEDURE IF EXISTS AddSubscription`,
	`DROP PROCEDURE IF EXISTS RemoveSubscription`,
	`DROP PROCEDURE IF EXISTS GetAllSubscriptions`,
	`DROP PROCEDURE IF EXISTS RemoveAllSubscriptions`,
}

var setupStatements = []string{
	`CREATE TABLE IF NOT EXISTS Mappings(
		ID BIGINT(64) NOT NULL PRIMARY KEY AUTO_INCREMENT,
		Name VARCHAR(20) NOT NULL UNIQUE,
		Twitter BIGINT(64) NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS Subscribers(
		ID BIGINT(64) NOT NULL PRIMARY KEY AUTO_INCREMENT,
		Chat BIGINT(64) NOT NULL,
		Mapping BIGINT(64) NOT NULL,
		FOREIGN KEY(Mapping) REFERENCES Mappings(ID)
	)`,
	`CREATE PROCEDURE IF NOT EXISTS CleanupRoutine()
	BEGIN
		START TRANSACTION;
			DELETE FROM Subscribers WHERE ID In (
				SELECT * FROM (
					SELECT S.ID FROM Subscribers S WHERE
						(SELECT COUNT(X.ID) FROM Subscribers X WHERE X.Chat = S.Chat AND X.Mapping = S.Mapping) > 1 AND
						(SELECT MIN(Y.ID) FROM Subscribers Y WHERE Y.Chat = S.Chat AND Y.Mapping = S.Mapping) <> S.ID
				) As Duplicates
			);
			DELETE FROM Mappings WHERE (SELECT COUNT(S.ID) FROM Subscribers S WHERE S.Mapping = ID) = 0;
		COMMIT;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS GetAllSubscriptions()
	BEGIN
		CALL CleanupRoutine();
		SELECT (SELECT COUNT(ID) FROM Mappings) As Amount, ID, Name, Twitter FROM Mappings;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS RemoveAllSubscriptions(ChatID BIGINT(64))
	BEGIN
		START TRANSACTION;
			DELETE FROM Subscribers WHERE Chat = ChatID;
			CALL CleanupRoutine();
		COMMIT;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS AddSubscription(ChatID BIGINT(64), Name VARCHAR(20))
	BEGIN
		SET @exists = COALESCE(
			(SELECT M.ID FROM Mappings M INNER JOIN Subscribers S ON S.Mapping = M.ID WHERE M.Name = Name AND S.Chat = ChatID LIMIT 1), 0
		);
		IF @exists = 0 THEN
			SET @mid = COALESCE((SELECT M.ID FROM Mappings M WHERE M.Name = Name LIMIT 1), 0);
			START TRANSACTION;
				IF @mid = 0 THEN
					INSERT INTO Mappings(Name) VALUES(Name);
					SET @mid = (SELECT M.ID FROM Mappings M WHERE M.Name = Name LIMIT 1);
				END IF;
				INSERT INTO Subscribers(Mapping, Chat) VALUES(@mid, ChatID);
			COMMIT;
		END IF;
		SELECT M.Twitter FROM Mappings M WHERE M.ID = @mid;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS RemoveSubscription(ChatID BIGINT(64), Name VARCHAR(20))
	BEGIN
		SET @mid = COALESCE(
			(SELECT M.ID FROM Mappings M INNER JOIN Subscribers S ON S.Mapping = M.ID WHERE M.Name = Name AND S.Chat = ChatID LIMIT 1), 0
		);
		IF @mid > 0 THEN
			START TRANSACTION;
				DELETE FROM Subscribers WHERE Mapping = @mid AND Chat = ChatID;
			COMMIT;
			SET @mid_count = COALESCE((SELECT COUNT(S.Mapping) FROM Subscribers S WHERE S.Mapping = @mid), 0);
			IF @mid_count > 0 THEN
				START TRANSACTION;
					DELETE FROM Mappings WHERE ID = @mid;
				COMMIT;
			END IF;
		END IF;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS UpdateMapping(MapID BIGINT(64), TwitterID BIGINT(64), Name VARCHAR(20))
	BEGIN
		SET @count = COALESCE((SELECT COUNT(M.ID) FROM Mappings M WHERE M.Twitter = TwitterID), 0);
		SET @exists = COALESCE((SELECT M.ID FROM Mappings M WHERE M.ID = MapID AND M.Name = Name LIMIT 1), 0);
		IF @exists = 0 THEN
			IF @count > 1 THEN
				START TRANSACTION;
					INSERT INTO Mappings(Name, Twitter) VALUES(CONCAT("!", Name), TwitterID);
					SET @mid = (SELECT M.ID FROM Mappings M WHERE M.Twitter = TwitterID AND M.Name = CONCAT("!", Name) LIMIT 1);
					UPDATE Subscribers S INNER JOIN Mappings M ON M.ID = S.Mapping SET S.Mapping = @mid WHERE M.Twitter = TwitterID;
					DELETE FROM Mappings WHERE Twitter = TwitterID AND ID != @mid;
					UPDATE Mappings SET Name = Name WHERE ID = @mid;
				COMMIT;
			ELSE
				START TRANSACTION;
					UPDATE Mappings M SET M.Twitter = TwitterID, M.Name = Name WHERE M.ID = MapID;
				COMMIT;
			END IF;
		ELSE
			START TRANSACTION;
				UPDATE Mappings M SET M.Twitter = TwitterID, M.Name = Name WHERE M.ID = MapID;
			COMMIT;
		END IF;
		CALL CleanupRoutine();
	END;`,
}

var queryStatements = map[string]string{
	"add":      `CALL AddSubscription(?, ?)`,
	"del":      `CALL RemoveSubscription(?, ?)`,
	"set":      `CALL UpdateMapping(?, ?, ?)`,
	"list":     `SELECT M.Name, M.Twitter FROM Mappings M INNER JOIN Subscribers S ON S.Mapping = M.ID WHERE S.Chat = ?`,
	"notify":   `SELECT S.Chat FROM Subscribers S INNER JOIN Mappings M ON M.ID = S.Mapping WHERE M.Twitter = ?`,
	"del_all":  `CALL RemoveAllSubscriptions(?)`,
	"get_all":  `CALL GetAllSubscriptions()`,
	"get_list": `SELECT (SELECT COUNT(ID) FROM Mappings) As Count, Twitter FROM Mappings`,
}
